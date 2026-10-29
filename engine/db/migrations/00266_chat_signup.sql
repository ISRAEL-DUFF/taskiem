-- Signing up from chat (docs/spec/chat-way-in.md 6, decision 0058).
--
-- A stranger on Taskiem's shared WhatsApp number or Telegram bot can make
-- a business from the chat. Until the business exists, the answers wait
-- in chat_signups, keyed by a digest of the channel address, for 30
-- minutes; the record belongs to a person, not a tenant, and only the
-- functions below reach it. Which terms and privacy notice a person
-- agreed to, when and where, is kept for every way in. Bindings gain a
-- dormant state: one silent for TASKIEM_BINDING_DORMANT_DAYS is sent
-- nothing and acts on nothing until it is proven again.
-- +goose Up
ALTER TABLE tenant_onboarding DROP CONSTRAINT tenant_onboarding_source_check;
ALTER TABLE tenant_onboarding ADD CONSTRAINT tenant_onboarding_source_check
  CHECK (source IN ('signup', 'existing', 'agent', 'whatsapp', 'telegram', 'slack'));
-- The digest of the identity a chat sign-up came from (its proven
-- number), for the per-identity limit.
ALTER TABLE tenant_onboarding ADD COLUMN channel_hash bytea CHECK (channel_hash IS NULL OR length(channel_hash) = 32);
CREATE INDEX tenant_onboarding_channel ON tenant_onboarding (channel_hash, signed_up_at) WHERE channel_hash IS NOT NULL;
CREATE INDEX tenant_onboarding_chat ON tenant_onboarding (signed_up_at) WHERE source IN ('whatsapp', 'telegram', 'slack');
-- The limits read across tenants, through taskiem_chat_signup_counts only.
CREATE POLICY dispatch_read ON tenant_onboarding FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (source, channel_hash, signed_up_at) ON tenant_onboarding TO taskiem_dispatch;

-- A sign-up in chat before there is a business: the step it is at and the
-- answers so far. One per address.
CREATE TABLE chat_signups (
  address_hash  bytea PRIMARY KEY CHECK (length(address_hash) = 32),
  channel       text NOT NULL CHECK (channel IN ('whatsapp', 'telegram')),
  step          text NOT NULL CHECK (length(step) BETWEEN 1 AND 32),
  data          jsonb NOT NULL DEFAULT '{}' CHECK (length(data::text) <= 4096),
  expires_at    timestamptz NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON chat_signups (expires_at);

-- The terms and privacy notice a person agreed to (or was asked to agree
-- to, once, when they had an account from before).
CREATE TABLE terms_acceptances (
  user_id      uuid NOT NULL REFERENCES users(id),
  document     text NOT NULL CHECK (document IN ('terms', 'privacy')),
  version      text NOT NULL CHECK (length(version) BETWEEN 1 AND 32),
  channel      text NOT NULL CHECK (channel IN ('web', 'whatsapp', 'telegram', 'slack', 'agent', 'invite')),
  asked_at     timestamptz NOT NULL DEFAULT now(),
  accepted_at  timestamptz,
  PRIMARY KEY (user_id, document, version)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['chat_signups', 'terms_acceptances'] LOOP
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

-- Dormant bindings: when each was last used, and since when it is
-- dormant. Linking again (a new verified_at) wakes it.
ALTER TABLE whatsapp_bindings ADD COLUMN last_active_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE whatsapp_bindings ADD COLUMN dormant_at timestamptz;
ALTER TABLE telegram_bindings ADD COLUMN last_active_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE telegram_bindings ADD COLUMN dormant_at timestamptz;

-- +goose StatementBegin
CREATE FUNCTION taskiem_binding_relinked() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.verified_at IS DISTINCT FROM OLD.verified_at THEN
    NEW.last_active_at := now();
    NEW.dormant_at := NULL;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER relinked BEFORE UPDATE ON whatsapp_bindings FOR EACH ROW EXECUTE FUNCTION taskiem_binding_relinked();
CREATE TRIGGER relinked BEFORE UPDATE ON telegram_bindings FOR EACH ROW EXECUTE FUNCTION taskiem_binding_relinked();

-- A sign-up's state at an address, while live. Now and then the expired
-- ones are cleared.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_signup_get(p_hash bytea)
RETURNS TABLE (step text, data jsonb) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF random() < 0.02 THEN
    DELETE FROM chat_signups x WHERE x.expires_at < now();
  END IF;
  RETURN QUERY SELECT x.step, x.data FROM chat_signups x WHERE x.address_hash = p_hash AND x.expires_at > now();
END
$$;
-- +goose StatementEnd

-- Starts or moves a sign-up on; it lives p_ttl from now.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_signup_put(p_hash bytea, p_channel text, p_step text, p_data jsonb, p_ttl interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO chat_signups AS x (address_hash, channel, step, data, expires_at) VALUES (p_hash, p_channel, p_step, p_data, now() + p_ttl)
  ON CONFLICT (address_hash) DO UPDATE SET channel = EXCLUDED.channel, step = EXCLUDED.step, data = EXCLUDED.data,
    expires_at = EXCLUDED.expires_at, updated_at = now()
$$;
-- +goose StatementEnd

-- Ends a sign-up (made, cancelled or refused).
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_signup_drop(p_hash bytea)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM chat_signups x WHERE x.address_hash = p_hash
$$;
-- +goose StatementEnd

-- The limits: businesses made from chat by one identity in the last day,
-- and by everyone in the last hour.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_signup_counts(p_hash bytea)
RETURNS TABLE (identity_day int, all_hour int) LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT (SELECT count(*)::int FROM tenant_onboarding o WHERE o.channel_hash = p_hash AND o.signed_up_at > now() - interval '1 day'),
         (SELECT count(*)::int FROM tenant_onboarding o WHERE o.source IN ('whatsapp', 'telegram', 'slack') AND o.signed_up_at > now() - interval '1 hour')
$$;
-- +goose StatementEnd

-- Binds a number to a person made or found by a chat sign-up, in the
-- sign-up's transaction: only a number that wrote in the last hour (the
-- sign-up is its conversation), bound to no one, for a person with no
-- number. Outcomes: ok, taken (the number is someone's), has (the person
-- has a number), quiet (the number has not written).
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_bind_whatsapp(p_user uuid, p_number text)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM whatsapp_contacts c WHERE c.number = p_number AND c.last_inbound_at > now() - interval '1 hour') THEN
    RETURN 'quiet';
  END IF;
  IF EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = p_number) THEN
    RETURN 'taken';
  END IF;
  IF EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.user_id = p_user) THEN
    RETURN 'has';
  END IF;
  INSERT INTO whatsapp_bindings (user_id, number) VALUES (p_user, p_number);
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- The same for a Telegram account.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_bind_telegram(p_user uuid, p_telegram_id bigint, p_username text)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM telegram_contacts c WHERE c.telegram_id = p_telegram_id AND c.last_inbound_at > now() - interval '1 hour') THEN
    RETURN 'quiet';
  END IF;
  IF EXISTS (SELECT 1 FROM telegram_bindings b WHERE b.telegram_id = p_telegram_id) THEN
    RETURN 'taken';
  END IF;
  IF EXISTS (SELECT 1 FROM telegram_bindings b WHERE b.user_id = p_user) THEN
    RETURN 'has';
  END IF;
  INSERT INTO telegram_bindings (user_id, telegram_id, username) VALUES (p_user, p_telegram_id, NULLIF(p_username, ''));
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- Whether a number shared on Telegram is already a WhatsApp-bound
-- person's (one person, many doors: linked, never a second account).
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_number_taken(p_number text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = p_number)
$$;
-- +goose StatementEnd

-- A bound person wrote on a channel ('whatsapp' or 'telegram'): their
-- binding is active (and now last used), or dormant (silent for p_days
-- or more, or dormant already: it stays so until proven again). With
-- p_days 0 nothing goes dormant.
-- +goose StatementBegin
CREATE FUNCTION taskiem_binding_touch(p_channel text, p_user uuid, p_days int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  last timestamptz;
  since timestamptz;
BEGIN
  IF p_channel = 'whatsapp' THEN
    SELECT b.last_active_at, b.dormant_at INTO last, since FROM whatsapp_bindings b WHERE b.user_id = p_user FOR UPDATE;
  ELSIF p_channel = 'telegram' THEN
    SELECT b.last_active_at, b.dormant_at INTO last, since FROM telegram_bindings b WHERE b.user_id = p_user FOR UPDATE;
  ELSE
    RAISE EXCEPTION 'no such channel' USING ERRCODE = '22023';
  END IF;
  IF NOT FOUND THEN
    RETURN 'none';
  END IF;
  IF since IS NOT NULL AND p_days > 0 THEN
    RETURN 'dormant';
  END IF;
  IF p_days > 0 AND last < now() - make_interval(days => p_days) THEN
    IF p_channel = 'whatsapp' THEN
      UPDATE whatsapp_bindings b SET dormant_at = now() WHERE b.user_id = p_user;
    ELSE
      UPDATE telegram_bindings b SET dormant_at = now() WHERE b.user_id = p_user;
    END IF;
    RETURN 'dormant';
  END IF;
  IF p_channel = 'whatsapp' THEN
    UPDATE whatsapp_bindings b SET last_active_at = now(), dormant_at = NULL WHERE b.user_id = p_user;
  ELSE
    UPDATE telegram_bindings b SET last_active_at = now(), dormant_at = NULL WHERE b.user_id = p_user;
  END IF;
  RETURN 'active';
END
$$;
-- +goose StatementEnd

-- A dormant binding proven again (the chat PIN): active from now.
-- +goose StatementBegin
CREATE FUNCTION taskiem_binding_wake(p_channel text, p_user uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  IF p_channel = 'whatsapp' THEN
    UPDATE whatsapp_bindings b SET last_active_at = now(), dormant_at = NULL WHERE b.user_id = p_user;
  ELSIF p_channel = 'telegram' THEN
    UPDATE telegram_bindings b SET last_active_at = now(), dormant_at = NULL WHERE b.user_id = p_user;
  ELSE
    RAISE EXCEPTION 'no such channel' USING ERRCODE = '22023';
  END IF;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n = 1;
END
$$;
-- +goose StatementEnd

-- The sweep: bindings silent p_days go dormant, and those dormant a
-- further p_days are removed (the address is a stranger again, and its
-- conversation starts afresh). Returns how many of each.
-- +goose StatementBegin
CREATE FUNCTION taskiem_bindings_dormant_sweep(p_days int)
RETURNS TABLE (dormant int, removed int) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  d1 int; d2 int; r1 int; r2 int;
  span interval := make_interval(days => p_days);
BEGIN
  IF p_days <= 0 THEN
    RETURN QUERY SELECT 0, 0; RETURN;
  END IF;
  UPDATE whatsapp_bindings b SET dormant_at = now() WHERE b.dormant_at IS NULL AND b.last_active_at < now() - span;
  GET DIAGNOSTICS d1 = ROW_COUNT;
  UPDATE telegram_bindings b SET dormant_at = now() WHERE b.dormant_at IS NULL AND b.last_active_at < now() - span;
  GET DIAGNOSTICS d2 = ROW_COUNT;
  WITH gone AS (DELETE FROM whatsapp_bindings b WHERE b.dormant_at < now() - span RETURNING b.number)
  UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number IN (SELECT number FROM gone);
  GET DIAGNOSTICS r1 = ROW_COUNT;
  WITH gone AS (DELETE FROM telegram_bindings b WHERE b.dormant_at < now() - span RETURNING b.telegram_id)
  UPDATE telegram_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.telegram_id IN (SELECT telegram_id FROM gone);
  GET DIAGNOSTICS r2 = ROW_COUNT;
  RETURN QUERY SELECT d1 + d2, r1 + r2;
END
$$;
-- +goose StatementEnd

-- Whether a person's binding on a channel is dormant (sends wait).
-- +goose StatementBegin
CREATE FUNCTION taskiem_binding_dormant(p_channel text, p_user uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT CASE p_channel
    WHEN 'whatsapp' THEN EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.user_id = p_user AND b.dormant_at IS NOT NULL)
    WHEN 'telegram' THEN EXISTS (SELECT 1 FROM telegram_bindings b WHERE b.user_id = p_user AND b.dormant_at IS NOT NULL)
    ELSE false END
$$;
-- +goose StatementEnd

-- Outbound: a dormant binding is sent nothing that carries a business's
-- data, so it is no one's address until it is proven again.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_numbers(p_users uuid[])
RETURNS TABLE (user_id uuid, number text, last_inbound_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.user_id, b.number, c.last_inbound_at FROM whatsapp_bindings b
    JOIN users u ON u.id = b.user_id AND u.status = 'active'
    LEFT JOIN whatsapp_contacts c ON c.number = b.number
   WHERE b.user_id = ANY (p_users) AND b.dormant_at IS NULL
     AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = b.user_id AND m.tenant_id = ANY (taskiem_tenant_scope()))
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_tg_accounts(p_users uuid[])
RETURNS TABLE (user_id uuid, telegram_id bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.user_id, b.telegram_id FROM telegram_bindings b
    JOIN users u ON u.id = b.user_id AND u.status = 'active'
   WHERE b.user_id = ANY (p_users) AND b.dormant_at IS NULL
     AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = b.user_id AND m.tenant_id = ANY (taskiem_tenant_scope()))
$$;
-- +goose StatementEnd

-- Terms: a person agreed, on a channel, to both documents' versions.
-- +goose StatementBegin
CREATE FUNCTION taskiem_terms_accept(p_user uuid, p_terms text, p_privacy text, p_channel text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO terms_acceptances AS a (user_id, document, version, channel, accepted_at)
  VALUES (p_user, 'terms', p_terms, p_channel, now()), (p_user, 'privacy', p_privacy, p_channel, now())
  ON CONFLICT (user_id, document, version) DO UPDATE SET channel = EXCLUDED.channel, accepted_at = now()
   WHERE a.accepted_at IS NULL
$$;
-- +goose StatementEnd

-- Whether a person agreed to both versions, and whether they were asked.
-- +goose StatementBegin
CREATE FUNCTION taskiem_terms_state(p_user uuid, p_terms text, p_privacy text)
RETURNS TABLE (accepted boolean, asked boolean) LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT count(*) FILTER (WHERE a.accepted_at IS NOT NULL) = 2, count(*) > 0
    FROM terms_acceptances a
   WHERE a.user_id = p_user AND ((a.document = 'terms' AND a.version = p_terms) OR (a.document = 'privacy' AND a.version = p_privacy))
$$;
-- +goose StatementEnd

-- Records that a person was asked, once: true the first time.
-- +goose StatementBegin
CREATE FUNCTION taskiem_terms_asked(p_user uuid, p_terms text, p_privacy text, p_channel text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  INSERT INTO terms_acceptances (user_id, document, version, channel)
  VALUES (p_user, 'terms', p_terms, p_channel), (p_user, 'privacy', p_privacy, p_channel)
  ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n > 0;
END
$$;
-- +goose StatementEnd

-- The chat PIN set from the chat, and a dormant binding woken by it.
ALTER TABLE whatsapp_flows DROP CONSTRAINT whatsapp_flows_kind_check;
ALTER TABLE whatsapp_flows ADD CONSTRAINT whatsapp_flows_kind_check CHECK (kind IN ('inputs', 'pin', 'public', 'pin_set', 'pin_wake'));

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_chat_signup_get(bytea)', 'taskiem_chat_signup_put(bytea, text, text, jsonb, interval)',
    'taskiem_chat_signup_drop(bytea)', 'taskiem_chat_signup_counts(bytea)', 'taskiem_chat_bind_whatsapp(uuid, text)',
    'taskiem_chat_bind_telegram(uuid, bigint, text)', 'taskiem_chat_number_taken(text)', 'taskiem_binding_touch(text, uuid, int)',
    'taskiem_binding_wake(text, uuid)', 'taskiem_bindings_dormant_sweep(int)', 'taskiem_binding_dormant(text, uuid)',
    'taskiem_terms_accept(uuid, text, text, text)', 'taskiem_terms_state(uuid, text, text)',
    'taskiem_terms_asked(uuid, text, text, text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DELETE FROM whatsapp_flows WHERE kind IN ('pin_set', 'pin_wake');
ALTER TABLE whatsapp_flows DROP CONSTRAINT whatsapp_flows_kind_check;
ALTER TABLE whatsapp_flows ADD CONSTRAINT whatsapp_flows_kind_check CHECK (kind IN ('inputs', 'pin', 'public'));
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_numbers(p_users uuid[])
RETURNS TABLE (user_id uuid, number text, last_inbound_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.user_id, b.number, c.last_inbound_at FROM whatsapp_bindings b
    JOIN users u ON u.id = b.user_id AND u.status = 'active'
    LEFT JOIN whatsapp_contacts c ON c.number = b.number
   WHERE b.user_id = ANY (p_users)
     AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = b.user_id AND m.tenant_id = ANY (taskiem_tenant_scope()))
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_tg_accounts(p_users uuid[])
RETURNS TABLE (user_id uuid, telegram_id bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.user_id, b.telegram_id FROM telegram_bindings b
    JOIN users u ON u.id = b.user_id AND u.status = 'active'
   WHERE b.user_id = ANY (p_users)
     AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = b.user_id AND m.tenant_id = ANY (taskiem_tenant_scope()))
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_terms_asked(uuid, text, text, text);
DROP FUNCTION taskiem_terms_state(uuid, text, text);
DROP FUNCTION taskiem_terms_accept(uuid, text, text, text);
DROP FUNCTION taskiem_binding_dormant(text, uuid);
DROP FUNCTION taskiem_bindings_dormant_sweep(int);
DROP FUNCTION taskiem_binding_wake(text, uuid);
DROP FUNCTION taskiem_binding_touch(text, uuid, int);
DROP FUNCTION taskiem_chat_number_taken(text);
DROP FUNCTION taskiem_chat_bind_telegram(uuid, bigint, text);
DROP FUNCTION taskiem_chat_bind_whatsapp(uuid, text);
DROP FUNCTION taskiem_chat_signup_counts(bytea);
DROP FUNCTION taskiem_chat_signup_drop(bytea);
DROP FUNCTION taskiem_chat_signup_put(bytea, text, text, jsonb, interval);
DROP FUNCTION taskiem_chat_signup_get(bytea);
DROP TRIGGER relinked ON telegram_bindings;
DROP TRIGGER relinked ON whatsapp_bindings;
DROP FUNCTION taskiem_binding_relinked();
ALTER TABLE telegram_bindings DROP COLUMN dormant_at;
ALTER TABLE telegram_bindings DROP COLUMN last_active_at;
ALTER TABLE whatsapp_bindings DROP COLUMN dormant_at;
ALTER TABLE whatsapp_bindings DROP COLUMN last_active_at;
DROP TABLE terms_acceptances;
DROP TABLE chat_signups;
REVOKE SELECT (source, channel_hash, signed_up_at) ON tenant_onboarding FROM taskiem_dispatch;
DROP POLICY dispatch_read ON tenant_onboarding;
DROP INDEX tenant_onboarding_chat;
DROP INDEX tenant_onboarding_channel;
ALTER TABLE tenant_onboarding DROP COLUMN channel_hash;
DELETE FROM tenant_onboarding WHERE source IN ('whatsapp', 'telegram', 'slack');
ALTER TABLE tenant_onboarding DROP CONSTRAINT tenant_onboarding_source_check;
ALTER TABLE tenant_onboarding ADD CONSTRAINT tenant_onboarding_source_check CHECK (source IN ('signup', 'existing', 'agent'));
