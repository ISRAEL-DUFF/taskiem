-- Signals are matched within the environment they were delivered to, so a
-- delivery verified with one environment's credentials cannot wake a run in
-- another. Schedule claims are shared fairly between tenants.

-- +goose Up
-- A waiting run's environment is its run's.
ALTER TABLE signal_waits ADD COLUMN environment text NOT NULL DEFAULT 'prod';
UPDATE signal_waits w SET environment = r.environment FROM runs r WHERE r.id = w.run_id AND w.environment <> r.environment;
ALTER TABLE signal_waits ALTER COLUMN environment DROP DEFAULT;
DROP INDEX signal_waits_match;
CREATE INDEX signal_waits_match ON signal_waits (tenant_id, environment, event, correlation);

-- Signals buffered before this migration were delivered to prod (the
-- default environment) or to an environment nothing recorded.
ALTER TABLE signals ADD COLUMN environment text NOT NULL DEFAULT 'prod';
ALTER TABLE signals ALTER COLUMN environment DROP DEFAULT;
DROP INDEX signals_match;
CREATE INDEX signals_match ON signals (tenant_id, environment, event, correlation, received_at);

-- At most p_per_tenant schedules of one tenant per batch: one tenant's
-- backlog cannot fill every batch.
DROP FUNCTION taskiem_claim_due_schedules(int, interval);
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_due_schedules(p_limit int, p_per_tenant int DEFAULT 10, p_lease interval DEFAULT '30 seconds')
RETURNS TABLE (trigger_id uuid, tenant_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE triggers t SET lease_until = now() + p_lease
   WHERE t.id IN (
     SELECT c.id FROM triggers c
      WHERE c.id IN (
        SELECT d.id FROM (
          SELECT id, row_number() OVER (PARTITION BY triggers.tenant_id ORDER BY next_fire_at, id) AS n
            FROM triggers
           WHERE type = 'schedule' AND next_fire_at <= now() AND (lease_until IS NULL OR lease_until < now())) d
         WHERE d.n <= p_per_tenant)
        AND c.type = 'schedule' AND c.next_fire_at <= now() AND (c.lease_until IS NULL OR c.lease_until < now())
      ORDER BY c.next_fire_at
      LIMIT p_limit
      FOR UPDATE SKIP LOCKED)
  RETURNING t.id, t.tenant_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_due_schedules(int, int, interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_due_schedules(int, int, interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_due_schedules(int, int, interval) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_claim_due_schedules(int, int, interval);
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_due_schedules(p_limit int, p_lease interval DEFAULT '30 seconds')
RETURNS TABLE (trigger_id uuid, tenant_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE triggers t SET lease_until = now() + p_lease
   WHERE t.id IN (
     SELECT id FROM triggers
      WHERE type = 'schedule' AND next_fire_at <= now() AND (lease_until IS NULL OR lease_until < now())
      ORDER BY next_fire_at
      LIMIT p_limit
      FOR UPDATE SKIP LOCKED)
  RETURNING t.id, t.tenant_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_due_schedules(int, interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_due_schedules(int, interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_due_schedules(int, interval) TO taskiem_app;

DROP INDEX signals_match;
CREATE INDEX signals_match ON signals (tenant_id, event, correlation, received_at);
ALTER TABLE signals DROP COLUMN environment;
DROP INDEX signal_waits_match;
CREATE INDEX signal_waits_match ON signal_waits (tenant_id, event, correlation);
ALTER TABLE signal_waits DROP COLUMN environment;
