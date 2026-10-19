-- Telegram as a chat channel beside WhatsApp (decision 0035, docs/telegram.md).
--
-- A person links one Telegram account to their Taskiem account by opening
-- a one-time t.me/<bot>?start=<code> link they made while signed in; the
-- bot then binds the Telegram user id that sent the code. As for WhatsApp
-- (00035), bindings, pending codes, the per-account conversation and the
-- updates already handled belong to people, not tenants: no tenant reads
-- them, and only the functions below reach them.
--
-- In the conversation tables the two channels share, a Telegram account's
-- address is tg:<user id> where a WhatsApp number is +<E.164>, so the two
-- can never collide; those tables' checks accept both.
-- +goose Up
CREATE TABLE telegram_bindings (
  user_id      uuid PRIMARY KEY REFERENCES users(id),
  telegram_id  bigint NOT NULL UNIQUE CHECK (telegram_id > 0),
  username     text CHECK (username IS NULL OR username ~ '^[A-Za-z0-9_]{1,64}$'),
  verified_at  timestamptz NOT NULL DEFAULT now()
);

-- One pending code per person, kept only as its SHA-256. Ten minutes,
-- single use; at most five codes an hour per person.
CREATE TABLE telegram_link_codes (
  user_id       uuid PRIMARY KEY REFERENCES users(id),
  code_hash     bytea NOT NULL UNIQUE CHECK (length(code_hash) = 32),
  expires_at    timestamptz NOT NULL,
  sends         int NOT NULL DEFAULT 1,
  window_start  timestamptz NOT NULL DEFAULT now()
);

-- Per Telegram account: the tenant it speaks to now, and when it last wrote.
CREATE TABLE telegram_contacts (
  telegram_id      bigint PRIMARY KEY CHECK (telegram_id > 0),
  current_tenant   uuid REFERENCES tenants(id),
  last_inbound_at  timestamptz,
  updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Update ids already handled: Telegram redelivers until it gets a 200.
CREATE TABLE telegram_inbound (
  update_id    bigint PRIMARY KEY,
  received_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON telegram_inbound (received_at);

-- Button payloads. Telegram's callback_data holds 64 bytes and decision
-- tokens are longer, so a button carries c:<id> and the payload stays
-- here, usable only from the account it was sent to.
CREATE TABLE telegram_callbacks (
  id           text PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9_-]{22}$'),
  telegram_id  bigint NOT NULL CHECK (telegram_id > 0),
  payload      text NOT NULL CHECK (length(payload) BETWEEN 1 AND 1024),
  expires_at   timestamptz NOT NULL
);
CREATE INDEX ON telegram_callbacks (expires_at);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['telegram_bindings', 'telegram_link_codes', 'telegram_contacts', 'telegram_inbound', 'telegram_callbacks'] LOOP
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

-- Issues a link code for a person: ok or too_many. A new code replaces the
-- pending one.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_code_issue(p_user uuid, p_code_hash bytea, p_ttl interval, p_max_sends int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r telegram_link_codes;
BEGIN
  SELECT * INTO r FROM telegram_link_codes c WHERE c.user_id = p_user FOR UPDATE;
  IF NOT FOUND THEN
    INSERT INTO telegram_link_codes (user_id, code_hash, expires_at) VALUES (p_user, p_code_hash, now() + p_ttl);
    RETURN 'ok';
  END IF;
  IF r.window_start > now() - interval '1 hour' THEN
    IF r.sends >= p_max_sends THEN
      RETURN 'too_many';
    END IF;
    UPDATE telegram_link_codes c SET code_hash = p_code_hash, expires_at = now() + p_ttl, sends = c.sends + 1 WHERE c.user_id = p_user;
  ELSE
    UPDATE telegram_link_codes c SET code_hash = p_code_hash, expires_at = now() + p_ttl, sends = 1, window_start = now() WHERE c.user_id = p_user;
  END IF;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- Binds the Telegram account that sent a code to the code's person:
-- invalid (no such live code), account_taken (bound to someone else), or
-- ok with the person. The code is spent; a person has one account, and
-- binding replaces theirs.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_code_redeem(p_code_hash bytea, p_telegram_id bigint, p_username text)
RETURNS TABLE (outcome text, user_id uuid) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  u uuid;
  old bigint;
BEGIN
  DELETE FROM telegram_link_codes c WHERE c.code_hash = p_code_hash AND c.expires_at > now() RETURNING c.user_id INTO u;
  IF u IS NULL OR NOT EXISTS (SELECT 1 FROM users x WHERE x.id = u AND x.status = 'active') THEN
    RETURN QUERY SELECT 'invalid'::text, NULL::uuid; RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM telegram_bindings b WHERE b.telegram_id = p_telegram_id AND b.user_id <> u) THEN
    RETURN QUERY SELECT 'account_taken'::text, NULL::uuid; RETURN;
  END IF;
  SELECT b.telegram_id INTO old FROM telegram_bindings b WHERE b.user_id = u;
  DELETE FROM telegram_bindings b WHERE b.user_id = u;
  INSERT INTO telegram_bindings (user_id, telegram_id, username) VALUES (u, p_telegram_id, NULLIF(p_username, ''));
  UPDATE telegram_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.telegram_id IN (p_telegram_id, old);
  RETURN QUERY SELECT 'ok'::text, u;
END
$$;
-- +goose StatementEnd

-- A person's binding and whether a code is pending, for their account page.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_binding(p_user uuid)
RETURNS TABLE (telegram_id bigint, username text, verified_at timestamptz, pending_expires_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.telegram_id, b.username, b.verified_at, c.expires_at
    FROM (SELECT p_user AS user_id) me
    LEFT JOIN telegram_bindings b ON b.user_id = me.user_id
    LEFT JOIN telegram_link_codes c ON c.user_id = me.user_id AND c.expires_at > now()
$$;
-- +goose StatementEnd

-- Who a Telegram account speaks for, if it is bound to an active person.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_user_of(p_telegram_id bigint)
RETURNS uuid LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.user_id FROM telegram_bindings b JOIN users u ON u.id = b.user_id AND u.status = 'active' WHERE b.telegram_id = p_telegram_id
$$;
-- +goose StatementEnd

-- Removes a person's binding; returns the account it held.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_unbind(p_user uuid)
RETURNS bigint LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH b AS (DELETE FROM telegram_bindings b WHERE b.user_id = p_user RETURNING b.telegram_id),
       c AS (UPDATE telegram_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.telegram_id IN (SELECT telegram_id FROM b))
  SELECT telegram_id FROM b
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_contact(p_telegram_id bigint)
RETURNS TABLE (current_tenant uuid, last_inbound_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.current_tenant, c.last_inbound_at FROM telegram_contacts c WHERE c.telegram_id = p_telegram_id
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_contact_touch(p_telegram_id bigint, p_inbound boolean, p_set_tenant boolean, p_tenant uuid)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO telegram_contacts AS c (telegram_id, current_tenant, last_inbound_at)
  VALUES (p_telegram_id, CASE WHEN p_set_tenant THEN p_tenant END, CASE WHEN p_inbound THEN now() END)
  ON CONFLICT (telegram_id) DO UPDATE SET
    current_tenant = CASE WHEN p_set_tenant THEN p_tenant ELSE c.current_tenant END,
    last_inbound_at = CASE WHEN p_inbound THEN now() ELSE c.last_inbound_at END,
    updated_at = now()
$$;
-- +goose StatementEnd

-- Claims an update id: true the first time. Ids older than eight days are
-- forgotten (Telegram keeps undelivered updates for 24 hours).
-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_inbound_claim(p_update_id bigint)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  IF random() < 0.01 THEN
    DELETE FROM telegram_inbound i WHERE i.received_at < now() - interval '8 days';
    DELETE FROM telegram_callbacks k WHERE k.expires_at < now();
  END IF;
  INSERT INTO telegram_inbound (update_id) VALUES (p_update_id) ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n = 1;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_callback_put(p_id text, p_telegram_id bigint, p_payload text, p_ttl interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO telegram_callbacks (id, telegram_id, payload, expires_at) VALUES (p_id, p_telegram_id, p_payload, now() + p_ttl)
$$;
-- +goose StatementEnd

-- A button's payload, only for the account it was sent to and while live.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_callback_get(p_id text, p_telegram_id bigint)
RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.payload FROM telegram_callbacks k WHERE k.id = p_id AND k.telegram_id = p_telegram_id AND k.expires_at > now()
$$;
-- +goose StatementEnd

-- Bound accounts of the given people, only for members of a tenant in the
-- caller's scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_accounts(p_users uuid[])
RETURNS TABLE (user_id uuid, telegram_id bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.user_id, b.telegram_id FROM telegram_bindings b
    JOIN users u ON u.id = b.user_id AND u.status = 'active'
   WHERE b.user_id = ANY (p_users)
     AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = b.user_id AND m.tenant_id = ANY (taskiem_tenant_scope()))
$$;
-- +goose StatementEnd

-- Tenants with at least one member who has linked Telegram.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tg_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT m.tenant_id FROM memberships m
    JOIN telegram_bindings b ON b.user_id = m.user_id
    JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_tg_code_issue(uuid, bytea, interval, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_code_redeem(bytea, bigint, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_binding(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_user_of(bigint) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_unbind(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_contact(bigint) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_contact_touch(bigint, boolean, boolean, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_inbound_claim(bigint) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_callback_put(text, bigint, text, interval) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_callback_get(text, bigint) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_accounts(uuid[]) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_tg_code_issue(uuid, bytea, interval, int), taskiem_tg_code_redeem(bytea, bigint, text), taskiem_tg_binding(uuid),
  taskiem_tg_user_of(bigint), taskiem_tg_unbind(uuid), taskiem_tg_contact(bigint), taskiem_tg_contact_touch(bigint, boolean, boolean, uuid),
  taskiem_tg_inbound_claim(bigint), taskiem_tg_callback_put(text, bigint, text, interval), taskiem_tg_callback_get(text, bigint),
  taskiem_tg_accounts(uuid[]), taskiem_tg_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_tg_code_issue(uuid, bytea, interval, int), taskiem_tg_code_redeem(bytea, bigint, text), taskiem_tg_binding(uuid),
  taskiem_tg_user_of(bigint), taskiem_tg_unbind(uuid), taskiem_tg_contact(bigint), taskiem_tg_contact_touch(bigint, boolean, boolean, uuid),
  taskiem_tg_inbound_claim(bigint), taskiem_tg_callback_put(text, bigint, text, interval), taskiem_tg_callback_get(text, bigint),
  taskiem_tg_accounts(uuid[]), taskiem_tg_tenants() TO taskiem_app;

-- The shared conversation tables take tg:<id> addresses beside numbers.
ALTER TABLE chat_sessions DROP CONSTRAINT chat_sessions_number_check;
ALTER TABLE chat_sessions ADD CONSTRAINT chat_sessions_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$');
ALTER TABLE channel_languages DROP CONSTRAINT channel_languages_number_check;
ALTER TABLE channel_languages ADD CONSTRAINT channel_languages_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$');
ALTER TABLE ai_builds DROP CONSTRAINT ai_builds_channel_check;
ALTER TABLE ai_builds ADD CONSTRAINT ai_builds_channel_check CHECK (channel IN ('web', 'whatsapp', 'telegram'));
ALTER TABLE voice_transcripts DROP CONSTRAINT voice_transcripts_channel_check;
ALTER TABLE voice_transcripts ADD CONSTRAINT voice_transcripts_channel_check CHECK (channel IN ('whatsapp', 'telegram'));
ALTER TABLE alert_channels DROP CONSTRAINT alert_channels_kind_check;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_kind_check CHECK (kind IN ('email', 'slack', 'webhook', 'whatsapp', 'telegram'));

-- Approval notices and the outbox are per channel: an approver linked on
-- both hears on both, and a decision on either closes the approval.
ALTER TABLE whatsapp_notices ADD COLUMN channel text NOT NULL DEFAULT 'whatsapp' CHECK (channel IN ('whatsapp', 'telegram'));
ALTER TABLE whatsapp_notices DROP CONSTRAINT whatsapp_notices_pkey;
ALTER TABLE whatsapp_notices ADD PRIMARY KEY (run_id, step_id, level, user_id, channel);
ALTER TABLE whatsapp_outbox ADD COLUMN channel text NOT NULL DEFAULT 'whatsapp' CHECK (channel IN ('whatsapp', 'telegram'));
ALTER TABLE whatsapp_run_watches ADD COLUMN channel text NOT NULL DEFAULT 'whatsapp' CHECK (channel IN ('whatsapp', 'telegram'));
-- The channel a decision or hand-off token was sent on: a decision
-- completed in the web app is recorded, and confirmed, on that channel.
ALTER TABLE whatsapp_tokens ADD COLUMN channel text NOT NULL DEFAULT 'whatsapp' CHECK (channel IN ('whatsapp', 'telegram'));

-- +goose Down
ALTER TABLE whatsapp_tokens DROP COLUMN channel;
ALTER TABLE whatsapp_run_watches DROP COLUMN channel;
ALTER TABLE whatsapp_outbox DROP COLUMN channel;
DELETE FROM whatsapp_notices WHERE channel = 'telegram';
ALTER TABLE whatsapp_notices DROP CONSTRAINT whatsapp_notices_pkey;
ALTER TABLE whatsapp_notices ADD PRIMARY KEY (run_id, step_id, level, user_id);
ALTER TABLE whatsapp_notices DROP COLUMN channel;
DELETE FROM alert_channels WHERE kind = 'telegram';
ALTER TABLE alert_channels DROP CONSTRAINT alert_channels_kind_check;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_kind_check CHECK (kind IN ('email', 'slack', 'webhook', 'whatsapp'));
DELETE FROM voice_transcripts WHERE channel = 'telegram';
ALTER TABLE voice_transcripts DROP CONSTRAINT voice_transcripts_channel_check;
ALTER TABLE voice_transcripts ADD CONSTRAINT voice_transcripts_channel_check CHECK (channel IN ('whatsapp'));
UPDATE ai_builds SET channel = 'web' WHERE channel = 'telegram';
ALTER TABLE ai_builds DROP CONSTRAINT ai_builds_channel_check;
ALTER TABLE ai_builds ADD CONSTRAINT ai_builds_channel_check CHECK (channel IN ('web', 'whatsapp'));
DELETE FROM channel_languages WHERE number LIKE 'tg:%';
ALTER TABLE channel_languages DROP CONSTRAINT channel_languages_number_check;
ALTER TABLE channel_languages ADD CONSTRAINT channel_languages_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$');
DELETE FROM chat_sessions WHERE number LIKE 'tg:%';
ALTER TABLE chat_sessions DROP CONSTRAINT chat_sessions_number_check;
ALTER TABLE chat_sessions ADD CONSTRAINT chat_sessions_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$');
DROP FUNCTION taskiem_tg_tenants();
DROP FUNCTION taskiem_tg_accounts(uuid[]);
DROP FUNCTION taskiem_tg_callback_get(text, bigint);
DROP FUNCTION taskiem_tg_callback_put(text, bigint, text, interval);
DROP FUNCTION taskiem_tg_inbound_claim(bigint);
DROP FUNCTION taskiem_tg_contact_touch(bigint, boolean, boolean, uuid);
DROP FUNCTION taskiem_tg_contact(bigint);
DROP FUNCTION taskiem_tg_unbind(uuid);
DROP FUNCTION taskiem_tg_user_of(bigint);
DROP FUNCTION taskiem_tg_binding(uuid);
DROP FUNCTION taskiem_tg_code_redeem(bytea, bigint, text);
DROP FUNCTION taskiem_tg_code_issue(uuid, bytea, interval, int);
DROP TABLE telegram_callbacks;
DROP TABLE telegram_inbound;
DROP TABLE telegram_contacts;
DROP TABLE telegram_link_codes;
DROP TABLE telegram_bindings;
