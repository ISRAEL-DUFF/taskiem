-- Alerts (spec 15.1): rules watch a tenant's runs, approvals, connectors,
-- credentials and audit anchors; what they find is recorded once and
-- delivered to email, Slack or a signed webhook, with retries.

-- +goose Up
CREATE TABLE alert_channels (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  kind        text NOT NULL CHECK (kind IN ('email', 'slack', 'webhook')),
  name        text NOT NULL,
  config      jsonb NOT NULL DEFAULT '{}',  -- email: {"to": [...]}; webhook: {"url": ...}. URLs with tokens and signing keys live in the vault
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  disabled_at timestamptz
);

CREATE TABLE alert_rules (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  name          text NOT NULL,
  kind          text NOT NULL CHECK (kind IN ('run_failed', 'slow_run', 'stuck_approval', 'needs_reconciliation', 'connector_drift', 'credential_expiry', 'audit_anchor')),
  config        jsonb NOT NULL DEFAULT '{}', -- workflow_id, environment, threshold
  channel_ids   uuid[] NOT NULL,
  enabled       boolean NOT NULL DEFAULT true,
  checked_until timestamptz NOT NULL DEFAULT now(), -- events after this are still to be looked at
  created_by    text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE alerts (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  rule_id     uuid REFERENCES alert_rules(id) ON DELETE SET NULL, -- NULL for a test message
  kind        text NOT NULL,
  dedup_key   text NOT NULL,
  title       text NOT NULL,
  body        text NOT NULL,
  link        text,
  detail      jsonb NOT NULL DEFAULT '{}',
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX alerts_once ON alerts (rule_id, dedup_key) WHERE rule_id IS NOT NULL;
CREATE INDEX alerts_recent ON alerts (tenant_id, created_at DESC);

CREATE TABLE alert_deliveries (
  alert_id        uuid NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
  channel_id      uuid NOT NULL REFERENCES alert_channels(id) ON DELETE CASCADE,
  tenant_id       uuid NOT NULL,
  status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_error      text,
  sent_at         timestamptz,
  PRIMARY KEY (alert_id, channel_id)
);
CREATE INDEX alert_deliveries_due ON alert_deliveries (next_attempt_at) WHERE status = 'pending';

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['alert_channels', 'alert_rules', 'alerts', 'alert_deliveries'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

CREATE POLICY dispatch ON alert_rules FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch ON alert_deliveries FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, enabled) ON alert_rules TO taskiem_dispatch;
GRANT SELECT (tenant_id, status, next_attempt_at) ON alert_deliveries TO taskiem_dispatch;

-- Tenants the alerter has work for, before any tenant scope exists.
-- +goose StatementBegin
CREATE FUNCTION taskiem_alert_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT r.tenant_id FROM alert_rules r WHERE r.enabled
  UNION
  SELECT d.tenant_id FROM alert_deliveries d WHERE d.status = 'pending' AND d.next_attempt_at <= now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_alert_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_alert_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_alert_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_alert_tenants();
DROP TABLE alert_deliveries;
DROP TABLE alerts;
DROP TABLE alert_rules;
DROP TABLE alert_channels;
