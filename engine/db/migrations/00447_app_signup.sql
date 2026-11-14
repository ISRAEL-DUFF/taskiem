-- Signing up in the phone app as on WhatsApp (docs/spec/mobile-app-m4.md
-- N7): a third way into chat sign-up, 'app'. The person proves their number
-- with a code sent on WhatsApp, then answers the same questions in the app's
-- own chat; the business is made as on WhatsApp, its owner bound to that
-- number. The code and the answers wait in chat_signups like the others'.
-- +goose Up
ALTER TABLE tenant_onboarding DROP CONSTRAINT tenant_onboarding_source_check;
ALTER TABLE tenant_onboarding ADD CONSTRAINT tenant_onboarding_source_check
  CHECK (source IN ('signup', 'existing', 'agent', 'whatsapp', 'telegram', 'slack', 'app'));
ALTER TABLE chat_signups DROP CONSTRAINT chat_signups_channel_check;
ALTER TABLE chat_signups ADD CONSTRAINT chat_signups_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack', 'app'));
ALTER TABLE terms_acceptances DROP CONSTRAINT terms_acceptances_channel_check;
ALTER TABLE terms_acceptances ADD CONSTRAINT terms_acceptances_channel_check
  CHECK (channel IN ('web', 'whatsapp', 'telegram', 'slack', 'agent', 'invite', 'app'));

-- The owner bound to the number the app proved with a code on WhatsApp:
-- taskiem_chat_bind_whatsapp's rules, with the code's proof in place of the
-- inbound message.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_bind_whatsapp_proven(p_user uuid, p_number text)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_proof bytea := sha256(convert_to('chat-signup:app-proven:' || p_number, 'UTF8'));
BEGIN
  -- The code was checked here (taskiem_app_signup_code_check): its proof is
  -- used up by the binding.
  DELETE FROM chat_signups x WHERE x.address_hash = v_proof AND x.expires_at > now();
  IF NOT FOUND THEN
    RETURN 'unproven';
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
ALTER FUNCTION taskiem_chat_bind_whatsapp_proven(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_chat_bind_whatsapp_proven(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_chat_bind_whatsapp_proven(uuid, text) TO taskiem_app;

-- A sign-up code checked and counted in one step, under a lock, so guesses
-- sent together are each counted: 'ok' (the code is used up, and the number
-- is proven for the next 40 minutes), 'wrong', 'locked' or 'none'. A wrong
-- guess does not make the code live longer.
-- +goose StatementBegin
CREATE FUNCTION taskiem_app_signup_code_check(p_number text, p_code_hash text, p_max int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_key bytea := sha256(convert_to('chat-signup:app-code:' || p_number, 'UTF8'));
  v_row chat_signups%ROWTYPE;
BEGIN
  SELECT * INTO v_row FROM chat_signups x WHERE x.address_hash = v_key AND x.expires_at > now() FOR UPDATE;
  IF NOT FOUND THEN
    RETURN 'none';
  END IF;
  IF coalesce((v_row.data->>'tries')::int, 0) >= p_max THEN
    RETURN 'locked';
  END IF;
  IF v_row.data->>'hash' IS DISTINCT FROM p_code_hash THEN
    UPDATE chat_signups x SET data = jsonb_set(x.data, '{tries}', to_jsonb(coalesce((x.data->>'tries')::int, 0) + 1))
     WHERE x.address_hash = v_key;
    RETURN 'wrong';
  END IF;
  DELETE FROM chat_signups x WHERE x.address_hash = v_key;
  INSERT INTO chat_signups AS x (address_hash, channel, step, data, expires_at)
  VALUES (sha256(convert_to('chat-signup:app-proven:' || p_number, 'UTF8')), 'app', 'proven', '{}', now() + interval '40 minutes')
  ON CONFLICT (address_hash) DO UPDATE SET expires_at = EXCLUDED.expires_at;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_app_signup_code_check(text, text, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_app_signup_code_check(text, text, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_app_signup_code_check(text, text, int) TO taskiem_app;

-- A phone app's device is the install that registered it: it is notified
-- only while that install has a session (signed out from Your phones, or
-- its session ended, it is not).
ALTER TABLE push_subscriptions ADD COLUMN device_id uuid;
-- +goose StatementBegin
CREATE FUNCTION taskiem_push_device_live(p_user uuid, p_device uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM sessions s WHERE s.user_id = p_user AND s.device_id = p_device AND s.revoked_at IS NULL AND s.expires_at > now())
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_push_device_live(uuid, uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_push_device_live(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_push_device_live(uuid, uuid) TO taskiem_app;
-- The install a session is (the app registering its device).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_session_device(p_hash bytea)
RETURNS uuid LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.device_id FROM sessions s WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_session_device(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_session_device(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_session_device(bytea) TO taskiem_app;

-- The push queue, as the audit found it should be: reactions are business
-- chats ('react:' groups), a newer push for a conversation takes the place
-- of an older one still waiting, and a retried push is not sent again to
-- the devices that took it (sent_to).
ALTER TABLE push_outbox ADD COLUMN sent_to uuid[] NOT NULL DEFAULT '{}';
GRANT SELECT (user_id, tag), DELETE ON push_outbox TO taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_push_kind(p_kind text, p_group text, p_link text) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE
    WHEN p_group = 'changes' OR p_group LIKE 'changes:%' OR p_kind = 'run_finished' OR p_group LIKE 'msg:%' THEN NULL
    WHEN p_group LIKE 'react:%' OR p_group LIKE 'reaction%' THEN 'business'
    WHEN p_group LIKE 'customer:%' THEN 'customers'
    WHEN p_group LIKE 'approval:%' THEN 'approvals'
    WHEN p_group LIKE 'order:%' OR p_group LIKE 'bill:%' OR p_group LIKE 'reminder:%' OR p_group LIKE 'notice:%' OR p_group = 'money'
      OR p_link LIKE '/bills%' OR p_link LIKE '/invoices%' OR p_link LIKE '/trade-orders%' OR p_link LIKE '/storefront/orders%' OR p_link LIKE '/purchases%' THEN 'orders'
    ELSE 'alerts'
  END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_push_from_notice() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_kind text;
  v_tag text;
BEGIN
  IF NEW.read_at IS NOT NULL OR (TG_OP = 'UPDATE' AND NEW.count = OLD.count) THEN
    RETURN NEW;
  END IF;
  v_kind := taskiem_push_kind(NEW.kind, NEW.group_key, NEW.link);
  IF v_kind IS NULL OR NOT EXISTS (SELECT 1 FROM push_subscriptions s WHERE s.tenant_id = NEW.tenant_id AND s.user_id = NEW.user_id) THEN
    RETURN NEW;
  END IF;
  -- A conversation's own tag (one notification per chat), else the group's.
  v_tag := COALESCE(substring(NEW.link FROM '^/chat\?with=([A-Za-z0-9-]{1,80})'), NEW.group_key);
  BEGIN
    -- A newer push for the same person and conversation takes the place of
    -- one still waiting (a retried older one would replace it on the phone).
    DELETE FROM push_outbox o WHERE o.tenant_id = NEW.tenant_id AND o.user_id = NEW.user_id AND o.tag = left(v_tag, 200);
    INSERT INTO push_outbox (id, tenant_id, user_id, kind, title, body, link, tag)
    VALUES (gen_random_uuid(), NEW.tenant_id, NEW.user_id, v_kind, left(NEW.title, 200), left(NEW.body, 1000),
            CASE WHEN NEW.link LIKE '/%' AND NEW.link NOT LIKE '//%' THEN left(NEW.link, 500) ELSE '/' END, left(v_tag, 200));
    PERFORM pg_notify('push_outbox', '');
  EXCEPTION WHEN OTHERS THEN
    RETURN NEW;
  END;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- A device the push service could not be reached for, day after day (its
-- address no longer resolves): dropped after a week, as one that refuses is
-- after three days; the service's trouble alone (busy, down) is not the
-- device's, but a week of nothing is.
ALTER TABLE push_subscriptions ADD COLUMN unreached_since timestamptz;

-- Pushes queued while this server sends none (its keys removed, devices
-- left): gone after their life (12 hours), so the queue does not grow.
-- +goose StatementBegin
CREATE FUNCTION taskiem_push_expire(p_ttl interval)
RETURNS bigint LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH gone AS (DELETE FROM push_outbox o WHERE o.created_at < now() - p_ttl RETURNING 1)
  SELECT count(*) FROM gone
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_push_expire(interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_push_expire(interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_push_expire(interval) TO taskiem_app;
GRANT SELECT (created_at) ON push_outbox TO taskiem_dispatch;

-- A customer's conversation muted (N2): no bell item and no push for it.
ALTER TABLE customer_conversations ADD COLUMN muted boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE customer_conversations DROP COLUMN muted;
REVOKE SELECT (created_at) ON push_outbox FROM taskiem_dispatch;
DROP FUNCTION taskiem_push_expire(interval);
ALTER TABLE push_subscriptions DROP COLUMN unreached_since;
REVOKE SELECT (user_id, tag), DELETE ON push_outbox FROM taskiem_dispatch;
ALTER TABLE push_outbox DROP COLUMN sent_to;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_push_kind(p_kind text, p_group text, p_link text) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE
    WHEN p_group = 'changes' OR p_group LIKE 'changes:%' OR p_kind = 'run_finished' OR p_group LIKE 'msg:%' THEN NULL
    WHEN p_group LIKE 'reaction%' THEN 'business'
    WHEN p_group LIKE 'customer:%' THEN 'customers'
    WHEN p_group LIKE 'approval:%' THEN 'approvals'
    WHEN p_group LIKE 'order:%' OR p_group LIKE 'bill:%' OR p_group LIKE 'reminder:%' OR p_group LIKE 'notice:%' OR p_group = 'money'
      OR p_link LIKE '/bills%' OR p_link LIKE '/invoices%' OR p_link LIKE '/trade-orders%' OR p_link LIKE '/storefront/orders%' OR p_link LIKE '/purchases%' THEN 'orders'
    ELSE 'alerts'
  END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_push_from_notice() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_kind text;
  v_tag text;
BEGIN
  IF NEW.read_at IS NOT NULL OR (TG_OP = 'UPDATE' AND NEW.count = OLD.count) THEN
    RETURN NEW;
  END IF;
  v_kind := taskiem_push_kind(NEW.kind, NEW.group_key, NEW.link);
  IF v_kind IS NULL OR NOT EXISTS (SELECT 1 FROM push_subscriptions s WHERE s.tenant_id = NEW.tenant_id AND s.user_id = NEW.user_id) THEN
    RETURN NEW;
  END IF;
  -- A conversation's own tag (one notification per chat), else the group's.
  v_tag := COALESCE(substring(NEW.link FROM '^/chat\?with=([A-Za-z0-9-]{1,80})'), NEW.group_key);
  BEGIN
    INSERT INTO push_outbox (id, tenant_id, user_id, kind, title, body, link, tag)
    VALUES (gen_random_uuid(), NEW.tenant_id, NEW.user_id, v_kind, left(NEW.title, 200), left(NEW.body, 1000),
            CASE WHEN NEW.link LIKE '/%' AND NEW.link NOT LIKE '//%' THEN left(NEW.link, 500) ELSE '/' END, left(v_tag, 200));
    PERFORM pg_notify('push_outbox', '');
  EXCEPTION WHEN OTHERS THEN
    RETURN NEW;
  END;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_auth_session_device(bytea);
DROP FUNCTION taskiem_push_device_live(uuid, uuid);
ALTER TABLE push_subscriptions DROP COLUMN device_id;
DROP FUNCTION taskiem_app_signup_code_check(text, text, int);
DROP FUNCTION taskiem_chat_bind_whatsapp_proven(uuid, text);
DELETE FROM chat_signups WHERE channel = 'app';
ALTER TABLE chat_signups DROP CONSTRAINT chat_signups_channel_check;
ALTER TABLE chat_signups ADD CONSTRAINT chat_signups_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));
UPDATE terms_acceptances SET channel = 'whatsapp' WHERE channel = 'app';
ALTER TABLE terms_acceptances DROP CONSTRAINT terms_acceptances_channel_check;
ALTER TABLE terms_acceptances ADD CONSTRAINT terms_acceptances_channel_check
  CHECK (channel IN ('web', 'whatsapp', 'telegram', 'slack', 'agent', 'invite'));
UPDATE tenant_onboarding SET source = 'whatsapp' WHERE source = 'app';
ALTER TABLE tenant_onboarding DROP CONSTRAINT tenant_onboarding_source_check;
ALTER TABLE tenant_onboarding ADD CONSTRAINT tenant_onboarding_source_check
  CHECK (source IN ('signup', 'existing', 'agent', 'whatsapp', 'telegram', 'slack'));
