-- A Git sync's claim is a fence (api/git.go). The claim now answers the
-- lease it set: the sync deploys, and writes its outcome, only while that
-- lease is still its own, so a sync whose claim lapsed and that another
-- api replica took over is deployed once. The pick is a materialised CTE:
-- as an IN subquery the planner may rescan it, and each rescan skips the
-- rows just locked, claiming more than p_limit (the api claims one at a
-- time).
-- +goose Up
DROP FUNCTION taskiem_claim_git_syncs(int, interval);
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_git_syncs(p_limit int, p_lease interval DEFAULT '5 minutes')
RETURNS TABLE (sync_id uuid, tenant_id uuid, lease_until timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH c AS MATERIALIZED (
    SELECT g.id FROM git_syncs g
     WHERE g.status = 'queued' OR (g.status = 'running' AND g.lease_until < now())
     ORDER BY g.requested_at
     LIMIT p_limit
     FOR UPDATE SKIP LOCKED)
  UPDATE git_syncs s SET status = 'running', lease_until = now() + p_lease, started_at = COALESCE(s.started_at, now())
    FROM c WHERE s.id = c.id
  RETURNING s.id, s.tenant_id, s.lease_until
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_git_syncs(int, interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_git_syncs(int, interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_git_syncs(int, interval) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_claim_git_syncs(int, interval);
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_git_syncs(p_limit int, p_lease interval DEFAULT '5 minutes')
RETURNS TABLE (sync_id uuid, tenant_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE git_syncs s SET status = 'running', lease_until = now() + p_lease, started_at = COALESCE(s.started_at, now())
   WHERE s.id IN (
     SELECT id FROM git_syncs
      WHERE status = 'queued' OR (status = 'running' AND lease_until < now())
      ORDER BY requested_at
      LIMIT p_limit
      FOR UPDATE SKIP LOCKED)
  RETURNING s.id, s.tenant_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_git_syncs(int, interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_git_syncs(int, interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_git_syncs(int, interval) TO taskiem_app;
