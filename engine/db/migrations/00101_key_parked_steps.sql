-- Fail closed, park, resume (decision 0019; docs/byok.md#when-the-key-is-unavailable).
--
-- key_parked_steps: steps a worker parked because the tenant's key could
-- not be unwrapped (a customer key revoked, disabled or unreachable, or the
-- platform KMS down). Nothing was sent for them in "send" mode; in
-- "reconcile" mode an earlier attempt may have been. When the key works
-- again the key job resumes each one (send: retry; reconcile: reconcile
-- first) without spending the step's retry budget, and deletes the row.
--
-- taskiem_key_tenants: the key job's routing: tenants with a customer key
-- to health-check, re-wrapping due, or parked steps. Ids only.
--
-- Alert rule kind key_health: a customer key became unavailable, or
-- recovered (docs/alerts.md).

-- +goose Up
CREATE TABLE key_parked_steps (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  run_id      uuid NOT NULL,
  step_id     text NOT NULL,
  attempt     int NOT NULL,
  mode        text NOT NULL CHECK (mode IN ('send', 'reconcile')),
  parked_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, run_id, step_id, attempt)
);
ALTER TABLE key_parked_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE key_parked_steps FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON key_parked_steps TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON key_parked_steps TO taskiem_app;
CREATE POLICY dispatch ON key_parked_steps FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id) ON key_parked_steps TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_key_tenants(p_now timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.tenant_id FROM tenant_byok_keys k WHERE k.status <> 'retired'
  UNION
  SELECT d.tenant_id FROM key_rewrap_due d WHERE d.not_before <= p_now
  UNION
  SELECT p.tenant_id FROM key_parked_steps p
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_key_tenants(timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_key_tenants(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_key_tenants(timestamptz) TO taskiem_app;

ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check CHECK (kind IN ('run_failed', 'slow_run', 'stuck_approval', 'needs_reconciliation',
  'connector_drift', 'credential_expiry', 'audit_anchor', 'limit', 'repair_proposed', 'key_health'));

-- +goose Down
DELETE FROM alert_deliveries d USING alerts a WHERE d.alert_id = a.id AND a.kind = 'key_health';
DELETE FROM alerts WHERE kind = 'key_health';
DELETE FROM alert_rules WHERE kind = 'key_health';
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check CHECK (kind IN ('run_failed', 'slow_run', 'stuck_approval', 'needs_reconciliation',
  'connector_drift', 'credential_expiry', 'audit_anchor', 'limit', 'repair_proposed'));
DROP FUNCTION taskiem_key_tenants(timestamptz);
REVOKE SELECT (tenant_id) ON key_parked_steps FROM taskiem_dispatch;
DROP TABLE key_parked_steps;
