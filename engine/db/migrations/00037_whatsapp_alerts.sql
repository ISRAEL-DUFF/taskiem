-- WhatsApp as an alert channel (spec 11.1 Notify): a channel names members
-- of the tenant, and each alert goes to those who have bound a number.

-- +goose Up
ALTER TABLE alert_channels DROP CONSTRAINT alert_channels_kind_check;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_kind_check CHECK (kind IN ('email', 'slack', 'webhook', 'whatsapp'));

-- +goose Down
DELETE FROM alert_deliveries d USING alert_channels c WHERE d.channel_id = c.id AND c.kind = 'whatsapp';
DELETE FROM alert_channels WHERE kind = 'whatsapp';
ALTER TABLE alert_channels DROP CONSTRAINT alert_channels_kind_check;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_kind_check CHECK (kind IN ('email', 'slack', 'webhook'));
