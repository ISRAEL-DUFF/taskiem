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

-- +goose Down
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
