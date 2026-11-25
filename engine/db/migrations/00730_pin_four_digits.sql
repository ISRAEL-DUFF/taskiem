-- The transaction PIN becomes four digits, and pays at once after a code
-- (docs/spec/transaction-pin.md; the founder's decisions of 25 November
-- 2026):
--
-- * Setting or changing it is confirmed by a one-time code to the person's
--   verified phone (phone_codes, purpose 'pin'), or by a passkey or an
--   authenticator; either way it pays at once. The 24-hour hold goes:
--   pays_from is now when it was set.
-- * Every PIN set before is six digits, which no pad takes any more: each
--   is removed, remembered as removed for that reason ('four_digits'), so
--   the person's settings say why it went and ask for a new one. Nothing
--   of the old PIN is kept.
-- +goose Up
ALTER TABLE phone_codes DROP CONSTRAINT phone_codes_purpose_check;
ALTER TABLE phone_codes ADD CONSTRAINT phone_codes_purpose_check CHECK (purpose IN ('sign_in', 'change_new', 'change_old', 'delete', 'pin'));

ALTER TABLE pin_removals ADD COLUMN why text NOT NULL DEFAULT 'removed' CHECK (why IN ('removed', 'four_digits'));

INSERT INTO pin_removals AS r (user_id, why) SELECT p.user_id, 'four_digits' FROM whatsapp_pins p
ON CONFLICT (user_id) DO UPDATE SET removed_at = now(), why = 'four_digits';
DELETE FROM whatsapp_pins;

ALTER TABLE whatsapp_pins ALTER COLUMN pays_from SET DEFAULT now();

-- Setting the PIN with no hold given: it pays at once.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_pin_set(p_user uuid, p_hash text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO whatsapp_pins AS p (user_id, pin_hash, pays_from) VALUES (p_user, p_hash, now())
  ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = now(), pays_from = now(),
    failures = 0, failed_since = NULL, locked_until = NULL
$$;
-- +goose StatementEnd

-- Removing it by hand is remembered as that.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_pin_remove(p_user uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM whatsapp_pins p WHERE p.user_id = p_user;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  INSERT INTO pin_removals AS r (user_id, why) VALUES (p_user, 'removed')
  ON CONFLICT (user_id) DO UPDATE SET removed_at = now(), why = 'removed';
  RETURN true;
END
$$;
-- +goose StatementEnd

-- Whether the person's last PIN went because PINs became four digits (and
-- none was set since): their settings ask for a new one.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pin_renew(p_user uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM pin_removals r WHERE r.user_id = p_user AND r.why = 'four_digits')
    AND NOT EXISTS (SELECT 1 FROM whatsapp_pins p WHERE p.user_id = p_user)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_pin_renew(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_pin_renew(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_pin_renew(uuid) TO taskiem_app;

-- +goose Down
-- The six-digit PINs removed are not brought back.
DROP FUNCTION taskiem_pin_renew(uuid);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_pin_remove(p_user uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM whatsapp_pins p WHERE p.user_id = p_user;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  INSERT INTO pin_removals AS r (user_id) VALUES (p_user)
  ON CONFLICT (user_id) DO UPDATE SET removed_at = now();
  RETURN true;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_pin_set(p_user uuid, p_hash text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO whatsapp_pins AS p (user_id, pin_hash, pays_from) VALUES (p_user, p_hash, now() + interval '24 hours')
  ON CONFLICT (user_id) DO UPDATE SET pin_hash = EXCLUDED.pin_hash, set_at = now(), pays_from = now() + interval '24 hours',
    failures = 0, failed_since = NULL, locked_until = NULL
$$;
-- +goose StatementEnd
ALTER TABLE whatsapp_pins ALTER COLUMN pays_from SET DEFAULT now() + interval '24 hours';
ALTER TABLE pin_removals DROP COLUMN why;
DELETE FROM phone_codes WHERE purpose = 'pin';
ALTER TABLE phone_codes DROP CONSTRAINT phone_codes_purpose_check;
ALTER TABLE phone_codes ADD CONSTRAINT phone_codes_purpose_check CHECK (purpose IN ('sign_in', 'change_new', 'change_old', 'delete'));
