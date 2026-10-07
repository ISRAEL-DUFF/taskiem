-- The AI repair pipeline (spec 12.2, decision 0016). A failed run, a step
-- parked in needs_reconciliation, or a first contract-drift finding queues
-- one repair job for the run, by trigger in the transaction that caused it
-- (an insert, nothing more: the analysis runs later, in the background).
-- The job classifies the failure, asks the model for a patch where the
-- class calls for one, proves the patch in a shadow sandbox, and stores
-- the outcome in repair_proposals. Nothing here publishes or resumes: a
-- person accepts a proposal through the API, under their own permissions.

-- +goose Up
-- Per-tenant switch: no row means on (when a model provider is configured).
CREATE TABLE ai_repair_settings (
  tenant_id  uuid PRIMARY KEY REFERENCES tenants(id),
  enabled    boolean NOT NULL DEFAULT true,
  updated_by text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE ai_repair_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_repair_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_repair_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON ai_repair_settings TO taskiem_app;

-- One job per (run, reason): a trigger firing twice queues nothing new.
CREATE TABLE repair_jobs (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  run_id       uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  reason       text NOT NULL CHECK (reason IN ('run_failed', 'needs_reconciliation', 'drift')),
  status       text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'skipped', 'failed')),
  attempts     int NOT NULL DEFAULT 0,
  available_at timestamptz NOT NULL DEFAULT now(),
  detail       text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  started_at   timestamptz,
  finished_at  timestamptz,
  UNIQUE (run_id, reason)
);
CREATE INDEX repair_jobs_due ON repair_jobs (tenant_id, available_at) WHERE status = 'queued';
ALTER TABLE repair_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE repair_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON repair_jobs TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch_queue ON repair_jobs FOR INSERT TO taskiem_dispatch WITH CHECK (true);
-- ON CONFLICT reads the arbiter's columns; the due list reads routing columns.
CREATE POLICY dispatch_read ON repair_jobs FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, UPDATE ON repair_jobs TO taskiem_app;
GRANT INSERT, SELECT (tenant_id, run_id, reason, status, available_at, started_at, attempts) ON repair_jobs TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_repair_queue(p_tenant uuid, p_run uuid, p_reason text, p_delay interval)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO repair_jobs (tenant_id, run_id, reason, available_at) VALUES (p_tenant, p_run, p_reason, now() + p_delay)
  ON CONFLICT (run_id, reason) DO NOTHING
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_repair_queue(uuid, uuid, text, interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_repair_queue(uuid, uuid, text, interval) FROM PUBLIC;

-- +goose StatementBegin
CREATE FUNCTION taskiem_repair_run_failed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_repair_queue(NEW.tenant_id, NEW.id, CASE NEW.status WHEN 'failed' THEN 'run_failed' ELSE 'needs_reconciliation' END, interval '0');
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_repair_run_failed() OWNER TO taskiem_dispatch;
CREATE TRIGGER runs_repair AFTER UPDATE OF status ON runs FOR EACH ROW
  WHEN (NEW.status IN ('failed', 'needs_reconciliation') AND OLD.status IS DISTINCT FROM NEW.status)
  EXECUTE FUNCTION taskiem_repair_run_failed();

-- A first finding (not a repeat) for a run: analysed once the run settles.
-- +goose StatementBegin
CREATE FUNCTION taskiem_repair_drift() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_repair_queue(NEW.tenant_id, NEW.last_run_id, 'drift', interval '0');
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_repair_drift() OWNER TO taskiem_dispatch;
CREATE TRIGGER connector_drift_repair AFTER INSERT ON connector_drift FOR EACH ROW
  WHEN (NEW.last_run_id IS NOT NULL)
  EXECUTE FUNCTION taskiem_repair_drift();

-- The repair loop: tenants with jobs due (routing columns only), and jobs
-- a stopped process left running for half an hour (up to three tries).
-- +goose StatementBegin
CREATE FUNCTION taskiem_repair_due_tenants(p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT j.tenant_id FROM repair_jobs j
   WHERE (j.status = 'queued' AND j.available_at <= now())
      OR (j.status = 'running' AND j.started_at < now() - interval '30 minutes' AND j.attempts < 3)
   LIMIT p_limit
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_repair_due_tenants(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_repair_due_tenants(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_repair_due_tenants(int) TO taskiem_app;

-- What came of a job: a proposed patch, a simple action, or a proposal
-- withheld because its shadow run did not pass.
CREATE TABLE repair_proposals (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  run_id            uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  job_id            uuid REFERENCES repair_jobs(id) ON DELETE SET NULL,
  workflow_id       uuid NOT NULL,
  version           int NOT NULL,                  -- the version the run failed on
  environment       text NOT NULL,
  reason            text NOT NULL,
  class             text CHECK (class IN ('transient', 'credential', 'data', 'schema_drift', 'logic', 'unknown_outcome')),
  classified_by     text CHECK (classified_by IN ('rules', 'model')),
  step_id           text,                          -- the failing (or parked) step instance
  status            text NOT NULL DEFAULT 'analysing' CHECK (status IN ('analysing', 'proposed', 'action', 'withheld', 'failed',
                      'awaiting_publish', 'awaiting_promotion', 'published', 'resumed', 'dismissed')),
  explanation       text NOT NULL DEFAULT '',
  action            jsonb,                         -- transient / credential / unknown_outcome: what a person can do
  definition        jsonb,                         -- the patched definition
  diff              jsonb,
  evidence          jsonb,                         -- shadow run, regression and existing tests, attempts
  test              jsonb,                         -- the regression case (wd-test/v1), redacted
  attempts          int NOT NULL DEFAULT 0,
  provider          text,
  model             text,
  total_tokens      bigint NOT NULL DEFAULT 0,
  error             text,
  created_by        text NOT NULL DEFAULT 'system:ai-repair',
  created_at        timestamptz NOT NULL DEFAULT now(),
  finished_at       timestamptz,
  draft_version     int,                           -- the patched version, once accepted
  accepted_by       text,                          -- the person who accepted (the human reviewer)
  accepted_at       timestamptz,
  dismissed_by      text,
  dismissed_at      timestamptz,
  resumed_run_id    uuid
);
CREATE INDEX repair_proposals_run ON repair_proposals (run_id, created_at DESC);
CREATE INDEX repair_proposals_workflow ON repair_proposals (workflow_id, created_at DESC);
CREATE INDEX repair_proposals_waiting ON repair_proposals (workflow_id, draft_version) WHERE status IN ('awaiting_publish', 'awaiting_promotion');
ALTER TABLE repair_proposals ENABLE ROW LEVEL SECURITY;
ALTER TABLE repair_proposals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON repair_proposals TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON repair_proposals TO taskiem_app;

-- Model calls made for a repair are logged and budgeted like builds.
ALTER TABLE ai_interactions ADD COLUMN repair_id uuid REFERENCES repair_proposals(id) ON DELETE SET NULL;
CREATE INDEX ai_interactions_repair ON ai_interactions (repair_id) WHERE repair_id IS NOT NULL;

-- A version created from a repair proposal: created_by is the system, the
-- proposal (with the person who accepted it) is the AI co-author.
ALTER TABLE workflow_versions ADD COLUMN repair_id uuid REFERENCES repair_proposals(id);

-- +goose Down
ALTER TABLE workflow_versions DROP COLUMN repair_id;
DROP INDEX ai_interactions_repair;
ALTER TABLE ai_interactions DROP COLUMN repair_id;
DROP TABLE repair_proposals;
DROP FUNCTION taskiem_repair_due_tenants(int);
DROP TRIGGER connector_drift_repair ON connector_drift;
DROP FUNCTION taskiem_repair_drift();
DROP TRIGGER runs_repair ON runs;
DROP FUNCTION taskiem_repair_run_failed();
DROP FUNCTION taskiem_repair_queue(uuid, uuid, text, interval);
DROP TABLE repair_jobs;
DROP TABLE ai_repair_settings;
