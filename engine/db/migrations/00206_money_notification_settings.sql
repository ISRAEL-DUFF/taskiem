-- Money summaries can be turned off, and on for members other than owners
-- (docs/money-reports.md, docs/alerts.md): a "money" group in Settings >
-- Notifications, whose chat is the daily summary and whose email is the
-- monthly email. Without a row, owners get both and others neither; only
-- members who can read invoices (invoice.read) see the group or get either.
-- +goose Up
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_kind_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_kind_check CHECK (kind IN ('failures', 'setup', 'finished', 'money'));

-- +goose Down
DELETE FROM notification_settings WHERE kind = 'money';
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_kind_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_kind_check CHECK (kind IN ('failures', 'setup', 'finished'));
