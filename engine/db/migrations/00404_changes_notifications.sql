-- The "changes" group (docs/spec/chat-tools.md §4): whether the changes a
-- person makes themselves (a price, stock, an order) are told on their
-- chat as diary lines. On by default; never by email.
-- +goose Up
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_kind_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_kind_check CHECK (kind IN ('failures', 'setup', 'finished', 'money', 'changes'));
-- +goose Down
DELETE FROM notification_settings WHERE kind = 'changes';
ALTER TABLE notification_settings DROP CONSTRAINT notification_settings_kind_check;
ALTER TABLE notification_settings ADD CONSTRAINT notification_settings_kind_check CHECK (kind IN ('failures', 'setup', 'finished', 'money'));
