-- Triggers registered from published workflow versions (spec 8), and
-- retention for approvals.

-- +goose Up
-- One row per (workflow, environment, trigger), replaced on every publish.
CREATE TABLE triggers (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  workflow_id   uuid NOT NULL,
  version       int  NOT NULL,
  environment   text NOT NULL,
  type          text NOT NULL CHECK (type IN ('webhook', 'schedule', 'connector_event')),
  -- webhook
  path          text,
  auth          text CHECK (auth IN ('hmac', 'bearer', 'none')),
  dedup         text,                              -- expression over body/headers/query
  secret_name   text,                              -- environment secret for hmac/bearer
  -- connector_event
  connector     text,                              -- "paystack@1"
  trigger_name  text,                              -- manifest trigger, e.g. transfer_event
  events        text[],
  connection    text,
  -- schedule
  cron          text,
  timezone      text,
  next_fire_at  timestamptz,
  lease_until   timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (workflow_id, version) REFERENCES workflow_versions (workflow_id, version),
  CHECK ((type = 'webhook') = (path IS NOT NULL)),
  CHECK ((type = 'schedule') = (cron IS NOT NULL AND next_fire_at IS NOT NULL)),
  CHECK ((type = 'connector_event') = (connector IS NOT NULL AND trigger_name IS NOT NULL))
);
CREATE UNIQUE INDEX triggers_webhook_path ON triggers (tenant_id, environment, path) WHERE type = 'webhook';
CREATE INDEX triggers_connector ON triggers (tenant_id, environment, connector, trigger_name) WHERE type = 'connector_event';
CREATE INDEX triggers_due ON triggers (next_fire_at) WHERE type = 'schedule';
CREATE INDEX triggers_workflow ON triggers (workflow_id);

ALTER TABLE triggers ENABLE ROW LEVEL SECURITY;
ALTER TABLE triggers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON triggers TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON triggers TO taskiem_app;

-- Schedulers lease due schedules across tenants (routing columns only).
CREATE POLICY dispatch ON triggers TO taskiem_dispatch USING (type = 'schedule') WITH CHECK (type = 'schedule');
GRANT SELECT (id, tenant_id, type, next_fire_at, lease_until), UPDATE (lease_until) ON triggers TO taskiem_dispatch;

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

-- Retention purges a run's approvals with it.
CREATE POLICY dispatch_purge ON approvals          FOR DELETE TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_purge ON approval_decisions FOR DELETE TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_select ON approvals          FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_select ON approval_decisions FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (run_id), DELETE ON approvals, approval_decisions TO taskiem_dispatch;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_purge_run(p_run uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_ok boolean;
BEGIN
  SELECT true INTO v_ok FROM runs
   WHERE id = p_run AND tenant_id = ANY (taskiem_tenant_scope())
     AND status IN ('completed', 'failed', 'cancelled') AND retain_until IS NOT NULL AND retain_until < now()
   FOR UPDATE;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  DELETE FROM run_events WHERE run_id = p_run;
  DELETE FROM tasks WHERE run_id = p_run;
  DELETE FROM timers WHERE run_id = p_run;
  DELETE FROM signal_waits WHERE run_id = p_run;
  DELETE FROM concurrency_slots WHERE run_id = p_run;
  DELETE FROM approval_decisions WHERE run_id = p_run;
  DELETE FROM approvals WHERE run_id = p_run;
  DELETE FROM runs WHERE id = p_run;
  RETURN true;
END
$$;
-- +goose StatementEnd
