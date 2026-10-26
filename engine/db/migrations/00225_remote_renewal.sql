-- Subscriptions that expire (decision 0051; docs/spec/business-connectors.md
-- K1). Some providers hold a subscription only for a while (Zoho CRM's
-- notification watches) and must be asked again before it lapses. A
-- registrar says when the provider's subscription expires; the reconciler
-- keeps it here and makes the row due before then, which applies the
-- subscription again (Update), renewing it.

-- +goose Up
ALTER TABLE remote_subscriptions ADD COLUMN expires_at timestamptz;

-- +goose Down
ALTER TABLE remote_subscriptions DROP COLUMN expires_at;
