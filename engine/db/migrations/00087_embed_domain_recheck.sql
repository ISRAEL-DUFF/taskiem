-- Custom domains are re-verified (docs/embedding.md#custom-domains): the
-- scheduler checks each verified domain's TXT record again periodically;
-- after repeated failures over a grace period the domain is unverified
-- (it stops routing), the partner is told by a domain.unverified webhook,
-- and both are audited.

-- +goose Up
ALTER TABLE embed_app_domains ADD COLUMN checked_at timestamptz,
  ADD COLUMN check_failures int NOT NULL DEFAULT 0,
  ADD COLUMN failing_since timestamptz,
  ADD COLUMN last_check_error text,
  ADD COLUMN unverified_at timestamptz;
CREATE INDEX embed_app_domains_recheck ON embed_app_domains (tenant_id, checked_at) WHERE verified_at IS NOT NULL;

ALTER TABLE partner_webhook_deliveries DROP CONSTRAINT partner_webhook_deliveries_event_check;
ALTER TABLE partner_webhook_deliveries ADD CONSTRAINT partner_webhook_deliveries_event_check
  CHECK (event IN ('run.completed', 'run.failed', 'workflow.published', 'usage.threshold', 'domain.unverified'));
ALTER TABLE embed_apps ALTER COLUMN webhook_events SET DEFAULT '{run.completed,run.failed,workflow.published,usage.threshold,domain.unverified}';

-- +goose Down
ALTER TABLE embed_apps ALTER COLUMN webhook_events SET DEFAULT '{run.completed,run.failed,workflow.published,usage.threshold}';
UPDATE embed_apps SET webhook_events = array_remove(webhook_events, 'domain.unverified');
DELETE FROM partner_webhook_deliveries WHERE event = 'domain.unverified';
ALTER TABLE partner_webhook_deliveries DROP CONSTRAINT partner_webhook_deliveries_event_check;
ALTER TABLE partner_webhook_deliveries ADD CONSTRAINT partner_webhook_deliveries_event_check
  CHECK (event IN ('run.completed', 'run.failed', 'workflow.published', 'usage.threshold'));
DROP INDEX embed_app_domains_recheck;
ALTER TABLE embed_app_domains DROP COLUMN checked_at, DROP COLUMN check_failures, DROP COLUMN failing_since,
  DROP COLUMN last_check_error, DROP COLUMN unverified_at;
