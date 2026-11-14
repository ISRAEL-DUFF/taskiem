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
-- taskiem_chat_bind_whatsapp's rules but the inbound message, which the
-- code stands for (the server checks it before calling this).
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_bind_whatsapp_proven(p_user uuid, p_number text)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
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

-- +goose Down
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
