-- Signing in on a new device without the PIN (docs/spec/taskiem-for-everyone.md
-- §5.4; founder, 25 November 2026). The PIN is for paying and approving on
-- WhatsApp only: it is no longer a second proof. The proofs are a passkey,
-- a link to the account's real email, or the password.
--
-- * taskiem_auth_second_proofs no longer says whether the person has a PIN.
--   It says whether the password was chosen by the person (set or reset by
--   them: users.password_chosen_at), which proves as a confirmed email
--   does (a password set by whoever added the person may not be theirs),
--   and whether the app asks the person for a way as a step
--   (users.sign_in_way_due).
-- * users.sign_in_way_due: an account made from now on, or one that has
--   never been opened on a phone app or browser (no device yet: a chat-born
--   person), is asked to add a way as a step before a business opens; an
--   account already in use is shown a reminder instead.
-- * Devices proved by the PIN before this change stay proved until their
--   proof lapses as any other (proved_by keeps 'pin').
-- +goose Up
ALTER TABLE users ADD COLUMN password_chosen_at timestamptz;
ALTER TABLE users ADD COLUMN sign_in_way_due boolean NOT NULL DEFAULT false;
UPDATE users u SET sign_in_way_due = true WHERE NOT EXISTS (SELECT 1 FROM user_devices d WHERE d.user_id = u.id);
ALTER TABLE users ALTER COLUMN sign_in_way_due SET DEFAULT true;
GRANT SELECT (password_chosen_at, sign_in_way_due) ON users TO taskiem_dispatch;

DROP FUNCTION taskiem_auth_second_proofs(uuid);
-- The ways a person could prove a new device is theirs: a passkey, a real
-- email (confirmed or not: the link goes to it), and a password (with
-- whether the email is confirmed and whether the person chose the
-- password, which the API weighs); and whether the app asks for one as a
-- step.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_second_proofs(p_user uuid)
RETURNS TABLE (passkey boolean, email boolean, password boolean, email_verified boolean, password_chosen boolean, way_due boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = p_user),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.email NOT LIKE '%.taskiem.invalid'),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.password_hash IS NOT NULL),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.email_verified_at IS NOT NULL),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.password_chosen_at IS NOT NULL),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.sign_in_way_due)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
  ALTER FUNCTION taskiem_auth_second_proofs(uuid) OWNER TO taskiem_dispatch;
  REVOKE EXECUTE ON FUNCTION taskiem_auth_second_proofs(uuid) FROM PUBLIC;
  GRANT EXECUTE ON FUNCTION taskiem_auth_second_proofs(uuid) TO taskiem_app;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_auth_second_proofs(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_second_proofs(p_user uuid)
RETURNS TABLE (passkey boolean, email boolean, pin boolean, password boolean, email_verified boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = p_user),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.email NOT LIKE '%.taskiem.invalid'),
         EXISTS (SELECT 1 FROM whatsapp_pins p WHERE p.user_id = p_user),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.password_hash IS NOT NULL),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.email_verified_at IS NOT NULL)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
  ALTER FUNCTION taskiem_auth_second_proofs(uuid) OWNER TO taskiem_dispatch;
  REVOKE EXECUTE ON FUNCTION taskiem_auth_second_proofs(uuid) FROM PUBLIC;
  GRANT EXECUTE ON FUNCTION taskiem_auth_second_proofs(uuid) TO taskiem_app;
END $$;
-- +goose StatementEnd
REVOKE SELECT (password_chosen_at, sign_in_way_due) ON users FROM taskiem_dispatch;
ALTER TABLE users DROP COLUMN sign_in_way_due;
ALTER TABLE users DROP COLUMN password_chosen_at;
