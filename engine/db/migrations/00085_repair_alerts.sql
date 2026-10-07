-- Alerts when the repair pipeline proposes a fix (docs/ai.md, docs/alerts.md):
-- a rule kind watching repair_proposals, delivered on the tenant's channels.

-- +goose Up
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check CHECK (kind IN ('run_failed', 'slow_run', 'stuck_approval', 'needs_reconciliation',
  'connector_drift', 'credential_expiry', 'audit_anchor', 'limit', 'repair_proposed'));
CREATE INDEX repair_proposals_finished ON repair_proposals (tenant_id, finished_at) WHERE status IN ('proposed', 'action');

-- +goose Down
DROP INDEX repair_proposals_finished;
DELETE FROM alert_deliveries d USING alerts a WHERE d.alert_id = a.id AND a.kind = 'repair_proposed';
DELETE FROM alerts WHERE kind = 'repair_proposed';
DELETE FROM alert_rules WHERE kind = 'repair_proposed';
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check CHECK (kind IN ('run_failed', 'slow_run', 'stuck_approval', 'needs_reconciliation',
  'connector_drift', 'credential_expiry', 'audit_anchor', 'limit'));
