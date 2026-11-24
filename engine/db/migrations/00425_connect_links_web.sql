-- Connecting from the Taskiem conversation on the web and in the phone app
-- (docs/spec/mobile-app.md 3.5): `connect <service>` there sends a connect
-- link as from WhatsApp, Telegram and Slack, and the conversation is told
-- when the connection is made (chatChannelFor's "web"). The channel's check
-- did not have it, so asking failed.
-- +goose Up
ALTER TABLE connect_links DROP CONSTRAINT connect_links_channel_check;
ALTER TABLE connect_links ADD CONSTRAINT connect_links_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'web'));

-- +goose Down
DELETE FROM connect_links WHERE channel = 'web';
ALTER TABLE connect_links DROP CONSTRAINT connect_links_channel_check;
ALTER TABLE connect_links ADD CONSTRAINT connect_links_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));
