-- The retention purge keeps up and shares its work (Atlas G18, "Risk:
-- found while writing this page"). Before this the claim took no lock, so
-- two schedulers archived the same runs, and one run that could not be
-- read or archived came first in every batch and stopped all purging.
--
-- runs.purge_after is when a run may next be claimed for the purge: a
-- claim sets it ten minutes ahead (the scheduler archives outside the
-- claim's transaction, so the lease is what keeps a second scheduler off
-- the run), and a failure sets it further ahead, doubling with each
-- attempt up to a day, so a run that keeps failing stops coming first.
-- purge_attempts and purge_error say why a run is still here.

-- +goose Up
ALTER TABLE runs
  ADD COLUMN purge_after    timestamptz,
  ADD COLUMN purge_attempts int NOT NULL DEFAULT 0,
  ADD COLUMN purge_error    text;

GRANT SELECT (purge_after, purge_attempts),
      UPDATE (purge_after, purge_attempts, purge_error) ON runs TO taskiem_dispatch;

-- Scheduler: claim ended runs past retention, across tenants (routing
-- only). SKIP LOCKED splits concurrent claims; the lease splits the
-- archiving that follows them.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_claim_purgeable_runs(p_limit int)
RETURNS TABLE (run_id uuid, tenant_id uuid)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE runs r SET purge_after = now() + interval '10 minutes'
    FROM (SELECT c.id FROM runs c
           WHERE c.retain_until IS NOT NULL AND c.retain_until < now()
             AND c.status IN ('completed', 'failed', 'cancelled')
             AND (c.purge_after IS NULL OR c.purge_after <= now())
           ORDER BY c.retain_until
           LIMIT p_limit
           FOR UPDATE SKIP LOCKED) c
   WHERE r.id = c.id
  RETURNING r.id, r.tenant_id
$$;
-- +goose StatementEnd

-- Records that a claimed run could not be purged and puts it back after a
-- backoff: 1 minute, doubling per attempt, at most a day. Only for a run
-- in the caller's tenant scope that is past retention, so it cannot hold
-- back anything else. Returns the attempts so far (0: no such run).
-- +goose StatementBegin
CREATE FUNCTION taskiem_purge_run_failed(p_run uuid, p_error text)
RETURNS int
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH f AS (
    UPDATE runs SET purge_attempts = purge_attempts + 1,
                    purge_error = left(p_error, 1000),
                    purge_after = now() + least(interval '1 minute' * power(2, least(purge_attempts, 11)), interval '1 day')
     WHERE id = p_run AND tenant_id = ANY (taskiem_tenant_scope())
       AND status IN ('completed', 'failed', 'cancelled') AND retain_until IS NOT NULL AND retain_until < now()
    RETURNING purge_attempts)
  SELECT COALESCE((SELECT purge_attempts FROM f), 0)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_purge_run_failed(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_purge_run_failed(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_purge_run_failed(uuid, text) TO taskiem_app;

-- The purge backlog, for the scheduler's metrics: ended runs past
-- retention not yet purged, how long the oldest has been overdue, and how
-- many of them have failed at least once. Counts only, across tenants.
-- +goose StatementBegin
CREATE FUNCTION taskiem_purge_backlog()
RETURNS TABLE (runs bigint, oldest_overdue_seconds float8, failing bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT count(*), COALESCE(extract(epoch FROM now() - min(r.retain_until)), 0)::float8,
         count(*) FILTER (WHERE r.purge_attempts > 0)
    FROM runs r
   WHERE r.retain_until IS NOT NULL AND r.retain_until < now()
     AND r.status IN ('completed', 'failed', 'cancelled')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_purge_backlog() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_purge_backlog() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_purge_backlog() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_purge_backlog();
DROP FUNCTION taskiem_purge_run_failed(uuid, text);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_claim_purgeable_runs(p_limit int)
RETURNS TABLE (run_id uuid, tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT r.id, r.tenant_id FROM runs r
   WHERE r.retain_until IS NOT NULL AND r.retain_until < now()
     AND r.status IN ('completed', 'failed', 'cancelled')
   ORDER BY r.retain_until
   LIMIT p_limit
$$;
-- +goose StatementEnd
REVOKE SELECT (purge_after, purge_attempts),
       UPDATE (purge_after, purge_attempts, purge_error) ON runs FROM taskiem_dispatch;
ALTER TABLE runs DROP COLUMN purge_error, DROP COLUMN purge_attempts, DROP COLUMN purge_after;
