-- The WhatsApp approval PIN (spec 11.2): a person may set a six-digit PIN
-- that, entered in a WhatsApp Flow bound to one decision, satisfies
-- approval policies whose step-up is "whatsapp_pin". Like the binding it is
-- the person's, not a tenant's: kept only as an Argon2id hash, reached only
-- through the functions below. Wrong PINs are counted, and too many in a
-- window lock it for a while, as authenticator codes do.

-- +goose Up
CREATE TABLE whatsapp_pins (
  user_id       uuid PRIMARY KEY REFERENCES users(id),
  pin_hash      text NOT NULL CHECK (pin_hash LIKE '$argon2id$%'),
  set_at        timestamptz NOT NULL DEFAULT now(),
  failures      int NOT NULL DEFAULT 0,
  failed_since  timestamptz,
  locked_until  timestamptz
);
ALTER TABLE whatsapp_pins ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_pins FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON whatsapp_pins TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON whatsapp_pins TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON whatsapp_pins FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON whatsapp_pins TO taskiem_dispatch;

-- Sets (or replaces) a person's PIN; its failures start afresh.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_pin_set(p_user uuid, p_hash text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO whatsapp_pins AS p (user_id, pin_hash) VALUES (p_user, p_hash)
  ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = now(), failures = 0, failed_since = NULL, locked_until = NULL
$$;
-- +goose StatementEnd

-- Removes a person's PIN; true when there was one.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_pin_remove(p_user uuid)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH d AS (DELETE FROM whatsapp_pins p WHERE p.user_id = p_user RETURNING 1) SELECT EXISTS (SELECT 1 FROM d)
$$;
-- +goose StatementEnd

-- A person's PIN: its hash (for checking), when it was set and until when
-- it is locked. No row when none is set.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_pin_state(p_user uuid)
RETURNS TABLE (pin_hash text, set_at timestamptz, locked_until timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.pin_hash, p.set_at, CASE WHEN p.locked_until > now() THEN p.locked_until END FROM whatsapp_pins p WHERE p.user_id = p_user
$$;
-- +goose StatementEnd

-- Records a check: a right PIN clears the failures; a wrong one counts in
-- the current window and, at p_max, locks the PIN for p_window. Returns
-- the failures counted and whether it is now locked.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_pin_result(p_user uuid, p_ok boolean, p_max int, p_window interval)
RETURNS TABLE (failures int, locked boolean) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  IF p_ok THEN
    UPDATE whatsapp_pins p SET failures = 0, failed_since = NULL WHERE p.user_id = p_user;
    RETURN QUERY SELECT 0, false; RETURN;
  END IF;
  UPDATE whatsapp_pins p SET
    failures = CASE WHEN p.failed_since > now() - p_window THEN p.failures + 1 ELSE 1 END,
    failed_since = CASE WHEN p.failed_since > now() - p_window THEN p.failed_since ELSE now() END
   WHERE p.user_id = p_user RETURNING p.failures INTO n;
  IF n IS NULL THEN
    RETURN QUERY SELECT 0, false; RETURN;
  END IF;
  IF n >= p_max THEN
    UPDATE whatsapp_pins p SET locked_until = now() + p_window, failures = 0, failed_since = NULL WHERE p.user_id = p_user;
    RETURN QUERY SELECT n, true; RETURN;
  END IF;
  RETURN QUERY SELECT n, false;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_wa_pin_set(uuid, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_pin_remove(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_pin_state(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_pin_result(uuid, boolean, int, interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_wa_pin_set(uuid, text), taskiem_wa_pin_remove(uuid), taskiem_wa_pin_state(uuid),
  taskiem_wa_pin_result(uuid, boolean, int, interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_wa_pin_set(uuid, text), taskiem_wa_pin_remove(uuid), taskiem_wa_pin_state(uuid),
  taskiem_wa_pin_result(uuid, boolean, int, interval) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_wa_pin_result(uuid, boolean, int, interval);
DROP FUNCTION taskiem_wa_pin_state(uuid);
DROP FUNCTION taskiem_wa_pin_remove(uuid);
DROP FUNCTION taskiem_wa_pin_set(uuid, text);
DROP TABLE whatsapp_pins;
