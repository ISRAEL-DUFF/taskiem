-- Linking a chat from the emailed link asks first (docs/spec/chat-way-in.md
-- 10.1). Anyone can type someone else's email into "I already use
-- Taskiem", so the link's page now shows the chat it would link (the
-- channel and its address, read here without using the link) and binds
-- only when the signed-in person says so and proves a factor. A chat
-- linked this way is remembered for a day (linked_at): for those 24 hours
-- it mints no web sign-in link and no password link.
-- +goose Up
ALTER TABLE chat_link_requests ADD COLUMN linked_at timestamptz;
CREATE INDEX chat_link_requests_linked ON chat_link_requests (user_id, channel) WHERE linked_at IS NOT NULL;

-- Records a link for an account; links an hour past their expiry go,
-- but one that linked a chat is kept two days, for the cool-off.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_chat_link_put(p_hash bytea, p_user uuid, p_channel text, p_address text, p_username text, p_ttl interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM chat_link_requests r WHERE r.expires_at < now() - interval '1 hour' AND (r.linked_at IS NULL OR r.linked_at < now() - interval '2 days');
  INSERT INTO chat_link_requests (token_hash, user_id, channel, address, username, expires_at)
  VALUES (p_hash, p_user, p_channel, p_address, NULLIF(p_username, ''), now() + p_ttl)
$$;
-- +goose StatementEnd

-- What a link would bind, without using it, for the account it was sent
-- to only: no row when it is unknown, used, expired or another account's.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_link_peek(p_hash bytea, p_user uuid)
RETURNS TABLE (channel text, address text, username text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT r.channel, r.address, r.username FROM chat_link_requests r
   WHERE r.token_hash = p_hash AND r.user_id = p_user AND r.used_at IS NULL AND r.expires_at > now()
$$;
-- +goose StatementEnd

-- Marks a taken link as having linked its chat: the cool-off starts.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_link_linked(p_hash bytea, p_user uuid)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE chat_link_requests r SET linked_at = now()
   WHERE r.token_hash = p_hash AND r.user_id = p_user AND r.used_at IS NOT NULL
$$;
-- +goose StatementEnd

-- When a person's chat on a channel, linked from an emailed link in the
-- last 24 hours, may again mint sign-in and password links; NULL when it
-- already may.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_link_cooling(p_user uuid, p_channel text)
RETURNS timestamptz LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT max(r.linked_at) + interval '24 hours' FROM chat_link_requests r
   WHERE r.user_id = p_user AND r.channel = p_channel AND r.linked_at > now() - interval '24 hours'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_chat_link_peek(bytea, uuid)', 'taskiem_chat_link_linked(bytea, uuid)', 'taskiem_chat_link_cooling(uuid, text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_chat_link_cooling(uuid, text);
DROP FUNCTION taskiem_chat_link_linked(bytea, uuid);
DROP FUNCTION taskiem_chat_link_peek(bytea, uuid);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_chat_link_put(p_hash bytea, p_user uuid, p_channel text, p_address text, p_username text, p_ttl interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM chat_link_requests r WHERE r.expires_at < now() - interval '1 hour';
  INSERT INTO chat_link_requests (token_hash, user_id, channel, address, username, expires_at)
  VALUES (p_hash, p_user, p_channel, p_address, NULLIF(p_username, ''), now() + p_ttl)
$$;
-- +goose StatementEnd
DROP INDEX chat_link_requests_linked;
ALTER TABLE chat_link_requests DROP COLUMN linked_at;
