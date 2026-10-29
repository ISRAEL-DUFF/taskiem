-- One person, many doors (docs/spec/chat-way-in.md 10, decision 0058).
--
-- "I already use Taskiem" from a chat no one is linked to asks for the
-- email the person signs in with and emails that account a one-time link;
-- opened while signed in to it, the link binds the chat (the WhatsApp
-- number, the Telegram account, the Slack user) to the account. A link
-- is kept only when the email has an account, so the chat's answer is the
-- same either way; it is a person's record, reached through the functions
-- below, and works once, for an hour, for that account alone.
--
-- A Telegram sign-up whose shared number is a WhatsApp-bound person's
-- joins that person when the account is chat-born (the number was its
-- only proof); otherwise the email link is the way.
--
-- An email added to an account whose sign-in name is a placeholder (or
-- not yet confirmed) is confirmed by its own link, which then replaces it.
-- +goose Up
CREATE TABLE chat_link_requests (
  token_hash  bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  user_id     uuid NOT NULL REFERENCES users(id),
  channel     text NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'slack')),
  address     text NOT NULL CHECK (length(address) BETWEEN 1 AND 64),
  username    text CHECK (length(username) <= 64),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz
);
CREATE INDEX ON chat_link_requests (expires_at);
ALTER TABLE chat_link_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_link_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON chat_link_requests TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON chat_link_requests TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON chat_link_requests FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON chat_link_requests TO taskiem_dispatch;

-- An email confirmation link that, once opened, makes its address the
-- account's (Add your email), not only confirms the current one.
ALTER TABLE email_verifications ADD COLUMN change boolean NOT NULL DEFAULT false;

-- Records a link for an account; links an hour past their expiry go.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_link_put(p_hash bytea, p_user uuid, p_channel text, p_address text, p_username text, p_ttl interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM chat_link_requests r WHERE r.expires_at < now() - interval '1 hour';
  INSERT INTO chat_link_requests (token_hash, user_id, channel, address, username, expires_at)
  VALUES (p_hash, p_user, p_channel, p_address, NULLIF(p_username, ''), now() + p_ttl)
$$;
-- +goose StatementEnd

-- Takes a link, once and in time, for the account it was sent to only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_link_take(p_hash bytea, p_user uuid)
RETURNS TABLE (channel text, address text, username text)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE chat_link_requests r SET used_at = now()
   WHERE r.token_hash = p_hash AND r.user_id = p_user AND r.used_at IS NULL AND r.expires_at > now()
  RETURNING r.channel, r.address, r.username
$$;
-- +goose StatementEnd

-- Binds a WhatsApp number or Telegram account to a person from a link
-- they opened: 'ok' (bound, or theirs already and now awake again),
-- 'taken' (someone else's), 'has' (they have another).
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_link_bind(p_user uuid, p_channel text, p_address text, p_username text)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  owner uuid;
BEGIN
  IF p_channel = 'whatsapp' THEN
    SELECT b.user_id INTO owner FROM whatsapp_bindings b WHERE b.number = p_address FOR UPDATE;
    IF owner = p_user THEN
      UPDATE whatsapp_bindings b SET verified_at = now() WHERE b.user_id = p_user;
      RETURN 'ok';
    ELSIF owner IS NOT NULL THEN
      RETURN 'taken';
    ELSIF EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.user_id = p_user) THEN
      RETURN 'has';
    END IF;
    INSERT INTO whatsapp_bindings (user_id, number) VALUES (p_user, p_address);
    UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number = p_address;
    RETURN 'ok';
  ELSIF p_channel = 'telegram' THEN
    SELECT b.user_id INTO owner FROM telegram_bindings b WHERE b.telegram_id = p_address::bigint FOR UPDATE;
    IF owner = p_user THEN
      UPDATE telegram_bindings b SET verified_at = now() WHERE b.user_id = p_user;
      RETURN 'ok';
    ELSIF owner IS NOT NULL THEN
      RETURN 'taken';
    ELSIF EXISTS (SELECT 1 FROM telegram_bindings b WHERE b.user_id = p_user) THEN
      RETURN 'has';
    END IF;
    INSERT INTO telegram_bindings (user_id, telegram_id, username) VALUES (p_user, p_address::bigint, NULLIF(p_username, ''));
    RETURN 'ok';
  END IF;
  RAISE EXCEPTION 'no such channel' USING ERRCODE = '22023';
END
$$;
-- +goose StatementEnd

-- The person a WhatsApp number is bound to, and whether their account is
-- chat-born (no password, a sign-in name that receives no mail): then the
-- number is all that proves them.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_number_owner(p_number text)
RETURNS TABLE (user_id uuid, chat_born boolean, email text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.id, u.password_hash IS NULL AND u.email LIKE '%.taskiem.invalid', u.email
    FROM whatsapp_bindings b JOIN users u ON u.id = b.user_id AND u.status = 'active'
   WHERE b.number = p_number
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_chat_link_put(bytea, uuid, text, text, text, interval)', 'taskiem_chat_link_take(bytea, uuid)',
    'taskiem_chat_link_bind(uuid, text, text, text)', 'taskiem_chat_number_owner(text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_chat_number_owner(text);
DROP FUNCTION taskiem_chat_link_bind(uuid, text, text, text);
DROP FUNCTION taskiem_chat_link_take(bytea, uuid);
DROP FUNCTION taskiem_chat_link_put(bytea, uuid, text, text, text, interval);
ALTER TABLE email_verifications DROP COLUMN change;
DROP TABLE chat_link_requests;
