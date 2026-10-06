-- Git integration (spec 10.3): one repository per environment, in
-- platform-led mode (publishing in Taskiem opens a pull request) or
-- Git-led mode (pushes to the branch deploy, and the UI is read-only for
-- the workflows the repository holds).

-- +goose Up
CREATE TABLE git_connections (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  provider     text NOT NULL CHECK (provider IN ('github', 'gitlab')),
  api_url      text NOT NULL DEFAULT '',      -- empty: github.com / gitlab.com
  repo         text NOT NULL,                 -- owner/name, or a GitLab project path
  branch       text NOT NULL,
  path         text NOT NULL DEFAULT 'flows', -- workflow definitions (*.wd.json, *.flow.ts)
  tests_path   text NOT NULL DEFAULT 'tests', -- workflow tests (*.test.json), also read under path
  mode         text NOT NULL CHECK (mode IN ('platform_led', 'git_led')),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment)
);

-- A Git-led deploy requested by a push webhook or by hand, worked by the
-- API role in the background.
CREATE TABLE git_syncs (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  environment   text NOT NULL,
  commit        text NOT NULL DEFAULT '',     -- empty: the branch head when it runs
  requested_by  text NOT NULL,                -- "webhook" or an actor
  status        text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'deployed', 'unchanged', 'failed')),
  report        jsonb,
  requested_at  timestamptz NOT NULL DEFAULT now(),
  started_at    timestamptz,
  finished_at   timestamptz,
  lease_until   timestamptz
);
CREATE INDEX git_syncs_pending ON git_syncs (requested_at) WHERE status IN ('queued', 'running');
CREATE INDEX git_syncs_tenant ON git_syncs (tenant_id, environment, requested_at DESC);

-- The pull or merge request a platform-led publish opened.
ALTER TABLE workflow_versions ADD COLUMN git_pr text;

ALTER TABLE git_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE git_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON git_connections TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON git_connections TO taskiem_app;

ALTER TABLE git_syncs ENABLE ROW LEVEL SECURITY;
ALTER TABLE git_syncs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON git_syncs TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON git_syncs TO taskiem_app;

-- The background worker leases pending syncs across tenants (routing
-- columns only); a sync whose lease lapsed (a crashed worker) is retried.
CREATE POLICY dispatch ON git_syncs TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (id, tenant_id, status, requested_at, lease_until, started_at), UPDATE (status, lease_until, started_at) ON git_syncs TO taskiem_dispatch;

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

-- +goose Down
DROP FUNCTION taskiem_claim_git_syncs(int, interval);
ALTER TABLE workflow_versions DROP COLUMN git_pr;
DROP TABLE git_syncs;
DROP TABLE git_connections;
