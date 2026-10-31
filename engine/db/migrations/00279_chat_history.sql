-- The Taskiem conversation's history (business links L3, §5.5): what a
-- member said to Taskiem and what Taskiem said to them, on WhatsApp,
-- Telegram, Slack and the web, so the web's Taskiem conversation shows
-- the same history as the phone. A product decision: the history
-- is kept on every channel. Before this nothing of the conversation was
-- kept (docs/whatsapp.md), and what is kept now is bounded:
--
-- - Each message is kept in the business it concerns (the business it
--   was said to or in whose name Taskiem spoke), and only when its
--   person is a member there: never a message to a customer, a rider or
--   a supplier, who are not having a conversation with Taskiem.
-- - The words are sealed under the business's subject keys (x-pii
--   category other); the buttons only by their titles (never their
--   tokens); a link keeps its host and path, not its query or fragment,
--   and a long opaque part of its path is cut; a message that is only a
--   code (a PIN, a sign-in code) is not kept.
-- - Each row expires (TASKIEM_CHAT_HISTORY_DAYS, 90 by default) and a
--   definer that reads only the expiry deletes it, as voice transcripts
--   are deleted (00146).
-- - Only the person reads their own history (the API filters by user).
-- +goose Up
CREATE TABLE chat_history (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  channel     text NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'web')),
  -- The person wrote it (else Taskiem did).
  inbound     boolean NOT NULL,
  -- What it was: text, a button pressed, a voice note, a photo, a form.
  kind        text NOT NULL DEFAULT 'text' CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location')),
  -- {"$pii": …}: the words, sealed.
  body        jsonb NOT NULL CHECK (body ? '$pii'),
  -- The buttons' titles, in order (never their ids, which carry tokens).
  buttons     text[] NOT NULL DEFAULT '{}' CHECK (cardinality(buttons) <= 10),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL
);
CREATE INDEX chat_history_person ON chat_history (tenant_id, user_id, created_at DESC);
CREATE INDEX chat_history_expiry ON chat_history (expires_at);
ALTER TABLE chat_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_history FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON chat_history TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON chat_history TO taskiem_app;

-- Retention across businesses: the dispatch role sees only when rows
-- expire and deletes those past it.
CREATE POLICY dispatch_purge ON chat_history FOR DELETE TO taskiem_dispatch USING (expires_at <= now());
CREATE POLICY dispatch_read_expiry ON chat_history FOR SELECT TO taskiem_dispatch USING (expires_at <= now());
GRANT SELECT (id, expires_at), DELETE ON chat_history TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_history_purge()
RETURNS bigint LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  DELETE FROM chat_history h WHERE h.expires_at <= now();
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_chat_history_purge() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_chat_history_purge() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_chat_history_purge() TO taskiem_app;

-- The web is a chat channel: its address is web:<user id>, in the tables
-- a conversation keeps (the session, the language), and a run started,
-- an approval asked or a notice told there is told on the web.
ALTER TABLE chat_sessions DROP CONSTRAINT chat_sessions_number_check;
ALTER TABLE chat_sessions ADD CONSTRAINT chat_sessions_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$' OR number ~ '^sl:[A-Z0-9]{1,24}:[A-Z0-9]{1,24}$'
  OR number ~ '^web:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');
ALTER TABLE channel_languages DROP CONSTRAINT channel_languages_number_check;
ALTER TABLE channel_languages ADD CONSTRAINT channel_languages_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$' OR number ~ '^sl:[A-Z0-9]{1,24}:[A-Z0-9]{1,24}$'
  OR number ~ '^web:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');
ALTER TABLE whatsapp_notices DROP CONSTRAINT whatsapp_notices_channel_check;
ALTER TABLE whatsapp_notices ADD CONSTRAINT whatsapp_notices_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'web'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_channel_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'web'));
ALTER TABLE whatsapp_run_watches DROP CONSTRAINT whatsapp_run_watches_channel_check;
ALTER TABLE whatsapp_run_watches ADD CONSTRAINT whatsapp_run_watches_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'web'));
ALTER TABLE whatsapp_tokens DROP CONSTRAINT whatsapp_tokens_channel_check;
ALTER TABLE whatsapp_tokens ADD CONSTRAINT whatsapp_tokens_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'web'));

-- +goose Down
DELETE FROM whatsapp_tokens WHERE channel = 'web';
ALTER TABLE whatsapp_tokens DROP CONSTRAINT whatsapp_tokens_channel_check;
ALTER TABLE whatsapp_tokens ADD CONSTRAINT whatsapp_tokens_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));
DELETE FROM whatsapp_run_watches WHERE channel = 'web';
ALTER TABLE whatsapp_run_watches DROP CONSTRAINT whatsapp_run_watches_channel_check;
ALTER TABLE whatsapp_run_watches ADD CONSTRAINT whatsapp_run_watches_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));
DELETE FROM whatsapp_outbox WHERE channel = 'web';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_channel_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));
DELETE FROM whatsapp_notices WHERE channel = 'web';
ALTER TABLE whatsapp_notices DROP CONSTRAINT whatsapp_notices_channel_check;
ALTER TABLE whatsapp_notices ADD CONSTRAINT whatsapp_notices_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));
DELETE FROM channel_languages WHERE number LIKE 'web:%';
ALTER TABLE channel_languages DROP CONSTRAINT channel_languages_number_check;
ALTER TABLE channel_languages ADD CONSTRAINT channel_languages_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$' OR number ~ '^sl:[A-Z0-9]{1,24}:[A-Z0-9]{1,24}$');
DELETE FROM chat_sessions WHERE number LIKE 'web:%';
ALTER TABLE chat_sessions DROP CONSTRAINT chat_sessions_number_check;
ALTER TABLE chat_sessions ADD CONSTRAINT chat_sessions_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$' OR number ~ '^sl:[A-Z0-9]{1,24}:[A-Z0-9]{1,24}$');
DROP FUNCTION taskiem_chat_history_purge();
DROP TABLE chat_history;
