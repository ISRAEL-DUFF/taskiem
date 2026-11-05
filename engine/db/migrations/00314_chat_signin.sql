-- +goose Up
-- Signing in on the web with the chat (docs/spec/chat-sign-in.md): a
-- six-digit code sent to a person's WhatsApp or Telegram from the login
-- page, or a one-time link "sign in" sends in chat. Both prove what
-- signing up in chat proved: the person holds that chat. A person may
-- turn it off for themselves (users.chat_signin). Codes and links belong
-- to a person, not a business: no row is reachable by the app role, only
-- through the definers below, as whatsapp_otps are.

ALTER TABLE sessions DROP CONSTRAINT sessions_auth_method_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_auth_method_check CHECK (auth_method IN ('password', 'passkey', 'sso', 'chat'));

ALTER TABLE users ADD COLUMN chat_signin boolean NOT NULL DEFAULT true;

-- The number a Telegram person shared, proven by Telegram's own-contact
-- check (sign-up asks for it): how the login page finds them by number.
-- People linked to Telegram before this, or from the web, have none and
-- sign in from chat with "sign in" instead.
ALTER TABLE telegram_bindings ADD COLUMN number text CHECK (number IS NULL OR number ~ '^\+[1-9][0-9]{6,14}$');
CREATE UNIQUE INDEX telegram_bindings_number ON telegram_bindings (number) WHERE number IS NOT NULL;
GRANT SELECT (chat_signin) ON users TO taskiem_dispatch;

-- One pending code per person, kept only as a SHA-256 of user:number:code,
-- so a code is good only for the number it was sent to. Ten minutes,
-- once; five wrong tries lock it; at most p_max_sends an hour.
CREATE TABLE chat_signin_codes (
  user_id       uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  number        text NOT NULL CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  channel       text NOT NULL CHECK (channel IN ('whatsapp', 'telegram')),
  code_hash     bytea NOT NULL CHECK (length(code_hash) = 32),
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  failures      int NOT NULL DEFAULT 0,
  sends         int NOT NULL DEFAULT 1,
  window_start  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX chat_signin_codes_number ON chat_signin_codes (number);

-- A one-time link sent in chat, kept only as the SHA-256 of its token.
CREATE TABLE chat_signin_links (
  token_hash  bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel     text NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'web')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz
);
CREATE INDEX chat_signin_links_user ON chat_signin_links (user_id, created_at);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['chat_signin_codes', 'chat_signin_links'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY no_direct_access ON %I TO taskiem_app USING (false) WITH CHECK (false)', t);
    EXECUTE format('CREATE POLICY dispatch ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', t);
    EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_dispatch', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- Who a number signs in: the person whose WhatsApp is bound to it, else
-- the one whose Telegram shared it; with their status, whether they allow
-- chat sign-in, and where a code can reach them.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_chat_user(p_number text)
RETURNS TABLE (user_id uuid, status text, chat_signin boolean, whatsapp boolean, telegram_id bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH found AS (
    SELECT b.user_id, true AS wa, 1 AS rank FROM whatsapp_bindings b WHERE b.number = p_number
    UNION ALL
    SELECT t.user_id, false, 2 FROM telegram_bindings t WHERE t.number = p_number
  ), one AS (
    SELECT f.user_id, f.wa FROM found f ORDER BY f.rank LIMIT 1
  )
  SELECT u.id, u.status, u.chat_signin, o.wa, (SELECT t.telegram_id FROM telegram_bindings t WHERE t.user_id = u.id)
    FROM one o JOIN users u ON u.id = o.user_id
$$;
-- +goose StatementEnd

-- Keeps the number a Telegram person shared, unless another's Telegram
-- has it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_telegram_number(p_user uuid, p_number text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE telegram_bindings t SET number = p_number
   WHERE t.user_id = p_user AND NOT EXISTS (SELECT 1 FROM telegram_bindings o WHERE o.number = p_number AND o.user_id <> p_user)
$$;
-- +goose StatementEnd

-- A person's status and whether they allow chat sign-in.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_chat_state(p_user uuid)
RETURNS TABLE (status text, chat_signin boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.status, u.chat_signin FROM users u WHERE u.id = p_user
$$;
-- +goose StatementEnd

-- Issues a code: ok, or too_many. A new code replaces the pending one and
-- its wrong tries, and counts toward the hour's sends.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_chat_code_issue(p_user uuid, p_number text, p_channel text, p_code text, p_ttl interval, p_max_sends int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r chat_signin_codes;
  h bytea := sha256(convert_to(p_user::text || ':' || p_number || ':' || p_code, 'UTF8'));
BEGIN
  SELECT * INTO r FROM chat_signin_codes c WHERE c.user_id = p_user FOR UPDATE;
  IF NOT FOUND THEN
    INSERT INTO chat_signin_codes (user_id, number, channel, code_hash, expires_at) VALUES (p_user, p_number, p_channel, h, now() + p_ttl);
    RETURN 'ok';
  END IF;
  IF r.window_start > now() - interval '1 hour' THEN
    IF r.sends >= p_max_sends THEN
      RETURN 'too_many';
    END IF;
    UPDATE chat_signin_codes c SET number = p_number, channel = p_channel, code_hash = h, created_at = now(), expires_at = now() + p_ttl,
      failures = 0, sends = c.sends + 1 WHERE c.user_id = p_user;
  ELSE
    UPDATE chat_signin_codes c SET number = p_number, channel = p_channel, code_hash = h, created_at = now(), expires_at = now() + p_ttl,
      failures = 0, sends = 1, window_start = now() WHERE c.user_id = p_user;
  END IF;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- Checks a code typed for a number: ok with the person (the code is spent),
-- or none, expired, locked or wrong (which counts toward the lock).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_chat_code_check(p_number text, p_code text, p_max_failures int)
RETURNS TABLE (outcome text, user_id uuid, channel text) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r chat_signin_codes;
BEGIN
  SELECT * INTO r FROM chat_signin_codes c WHERE c.number = p_number ORDER BY c.created_at DESC LIMIT 1 FOR UPDATE;
  IF NOT FOUND THEN
    RETURN QUERY SELECT 'none'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  IF r.expires_at <= now() THEN
    RETURN QUERY SELECT 'expired'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  IF r.failures >= p_max_failures THEN
    RETURN QUERY SELECT 'locked'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  IF r.code_hash <> sha256(convert_to(r.user_id::text || ':' || r.number || ':' || p_code, 'UTF8')) THEN
    UPDATE chat_signin_codes c SET failures = c.failures + 1 WHERE c.user_id = r.user_id;
    RETURN QUERY SELECT 'wrong'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  -- Spent; the hour's sends are kept.
  UPDATE chat_signin_codes c SET expires_at = now(), failures = p_max_failures WHERE c.user_id = r.user_id;
  RETURN QUERY SELECT 'ok'::text, r.user_id, r.channel;
END
$$;
-- +goose StatementEnd

-- Issues a link: false when the person has asked for p_max this hour.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_chat_link_issue(p_user uuid, p_hash bytea, p_channel text, p_ttl interval, p_max int)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF (SELECT count(*) FROM chat_signin_links l WHERE l.user_id = p_user AND l.created_at > now() - interval '1 hour') >= p_max THEN
    RETURN false;
  END IF;
  DELETE FROM chat_signin_links l WHERE l.user_id = p_user AND l.created_at < now() - interval '1 day';
  INSERT INTO chat_signin_links (token_hash, user_id, channel, expires_at) VALUES (p_hash, p_user, p_channel, now() + p_ttl);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- Uses a link once: its person and channel, or no row when it is unknown,
-- used or expired.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_chat_link_use(p_hash bytea)
RETURNS TABLE (user_id uuid, channel text) LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE chat_signin_links l SET used_at = now()
   WHERE l.token_hash = p_hash AND l.used_at IS NULL AND l.expires_at > now()
  RETURNING l.user_id, l.channel
$$;
-- +goose StatementEnd

-- Whether a link is still good, without using it: for its page, which
-- says so before the button. Nothing about the person.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_chat_link_peek(p_hash bytea)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM chat_signin_links l WHERE l.token_hash = p_hash AND l.used_at IS NULL AND l.expires_at > now())
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_chat_user(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_chat_state(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_chat_telegram_number(uuid, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_chat_code_issue(uuid, text, text, text, interval, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_chat_code_check(text, text, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_chat_link_issue(uuid, bytea, text, interval, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_chat_link_use(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_chat_link_peek(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_chat_user(text), taskiem_auth_chat_state(uuid), taskiem_chat_telegram_number(uuid, text),
  taskiem_auth_chat_code_issue(uuid, text, text, text, interval, int), taskiem_auth_chat_code_check(text, text, int),
  taskiem_auth_chat_link_issue(uuid, bytea, text, interval, int), taskiem_auth_chat_link_use(bytea), taskiem_auth_chat_link_peek(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_chat_user(text), taskiem_auth_chat_state(uuid), taskiem_chat_telegram_number(uuid, text),
  taskiem_auth_chat_code_issue(uuid, text, text, text, interval, int), taskiem_auth_chat_code_check(text, text, int),
  taskiem_auth_chat_link_issue(uuid, bytea, text, interval, int), taskiem_auth_chat_link_use(bytea), taskiem_auth_chat_link_peek(bytea) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_chat_link_peek(bytea);
DROP FUNCTION taskiem_auth_chat_link_use(bytea);
DROP FUNCTION taskiem_auth_chat_link_issue(uuid, bytea, text, interval, int);
DROP FUNCTION taskiem_auth_chat_code_check(text, text, int);
DROP FUNCTION taskiem_auth_chat_code_issue(uuid, text, text, text, interval, int);
DROP FUNCTION taskiem_auth_chat_state(uuid);
DROP FUNCTION taskiem_auth_chat_user(text);
DROP FUNCTION taskiem_chat_telegram_number(uuid, text);
DROP INDEX telegram_bindings_number;
ALTER TABLE telegram_bindings DROP COLUMN number;
DROP TABLE chat_signin_links;
DROP TABLE chat_signin_codes;
REVOKE SELECT (chat_signin) ON users FROM taskiem_dispatch;
ALTER TABLE users DROP COLUMN chat_signin;
UPDATE sessions SET revoked_at = COALESCE(revoked_at, now()), auth_method = 'password' WHERE auth_method = 'chat';
ALTER TABLE sessions DROP CONSTRAINT sessions_auth_method_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_auth_method_check CHECK (auth_method IN ('password', 'passkey', 'sso'));
