-- +goose Up
-- Chat tools (docs/spec/chat-tools.md): a business's own switch for the
-- new chat, built in phases behind it. When on, who a message goes to is
-- decided by its recipients: a message naming linked businesses goes to
-- them, and "@Taskiem" calls Taskiem; the "..." marker still works. Off
-- by default: a business turns it on in Settings.
ALTER TABLE tenants ADD COLUMN chat_tools boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE tenants DROP COLUMN chat_tools;
