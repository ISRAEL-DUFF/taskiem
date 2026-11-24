-- The PIN set at sign-up and reset without texting it (chat-pay-people,
-- Settled row 3): a PIN set behind a passkey or an authenticator pays at
-- once; one set without them (the first PIN, at sign-up or from the chat,
-- or a forgotten one reset by signing in again) pays only 24 hours on.
-- pays_from says when; approvals by PIN are unchanged.
-- +goose Up
ALTER TABLE whatsapp_pins ADD COLUMN pays_from timestamptz;
UPDATE whatsapp_pins SET pays_from = set_at + interval '24 hours';
ALTER TABLE whatsapp_pins ALTER COLUMN pays_from SET NOT NULL;
ALTER TABLE whatsapp_pins ALTER COLUMN pays_from SET DEFAULT now() + interval '24 hours';

-- Setting the PIN without a hold keeps the earlier rule: 24 hours.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_pin_set(p_user uuid, p_hash text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO whatsapp_pins AS p (user_id, pin_hash, pays_from) VALUES (p_user, p_hash, now() + interval '24 hours')
  ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = now(), pays_from = now() + interval '24 hours',
    failures = 0, failed_since = NULL, locked_until = NULL
$$;
-- +goose StatementEnd

-- Sets (or replaces) a person's PIN, paying p_hold from now (0 behind a
-- passkey or an authenticator, 24 hours without).
-- +goose StatementBegin
CREATE FUNCTION taskiem_pin_set_hold(p_user uuid, p_hash text, p_hold interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO whatsapp_pins AS p (user_id, pin_hash, pays_from) VALUES (p_user, p_hash, now() + p_hold)
  ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = now(), pays_from = now() + p_hold,
    failures = 0, failed_since = NULL, locked_until = NULL
$$;
-- +goose StatementEnd

-- When a person's PIN may pay from; no row when none is set.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pin_pays_from(p_user uuid)
RETURNS timestamptz LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.pays_from FROM whatsapp_pins p WHERE p.user_id = p_user
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_pin_set_hold(uuid, text, interval) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_pin_pays_from(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_pin_set_hold(uuid, text, interval), taskiem_pin_pays_from(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_pin_set_hold(uuid, text, interval), taskiem_pin_pays_from(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_pin_pays_from(uuid);
DROP FUNCTION taskiem_pin_set_hold(uuid, text, interval);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_pin_set(p_user uuid, p_hash text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO whatsapp_pins AS p (user_id, pin_hash) VALUES (p_user, p_hash)
  ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = now(), failures = 0, failed_since = NULL, locked_until = NULL
$$;
-- +goose StatementEnd
ALTER TABLE whatsapp_pins DROP COLUMN pays_from;
