-- A sign-in by a code to the phone or a chat never inherits an earlier
-- second proof (docs/spec/taskiem-for-everyone.md §5.4, review of the
-- review fixes). The app's device id is chosen by the app, so it is not
-- a secret: whoever holds a reissued number and learns the id of the
-- owner's proved phone could sign in by code presenting it and take its
-- proof. Each sign-in by code now starts unproved, on every device; the
-- person proves once more (a passkey, an email link, the chat PIN or the
-- password). Both callers (api/phone_signin.go and api/chat_signin.go)
-- are sign-ins by code.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_device_signin(p_user uuid, p_device bytea, p_phone boolean)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_phone THEN
    UPDATE user_phones p SET last_signin_at = now() WHERE p.user_id = p_user;
  END IF;
  IF p_device IS NULL THEN
    RETURN false;
  END IF;
  INSERT INTO user_devices AS d (user_id, device_hash) VALUES (p_user, p_device)
  ON CONFLICT (user_id, device_hash) DO UPDATE
    SET proved_at = NULL, proved_by = NULL, last_signin_at = now();
  RETURN false;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_device_signin(p_user uuid, p_device bytea, p_phone boolean)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_stale boolean := false;
  v_proved timestamptz;
BEGIN
  IF p_phone THEN
    SELECT p.last_signin_at < now() - interval '90 days' INTO v_stale FROM user_phones p WHERE p.user_id = p_user;
    UPDATE user_phones p SET last_signin_at = now() WHERE p.user_id = p_user;
  END IF;
  IF p_device IS NULL THEN
    RETURN false;
  END IF;
  INSERT INTO user_devices AS d (user_id, device_hash) VALUES (p_user, p_device)
  ON CONFLICT (user_id, device_hash) DO UPDATE
    SET proved_at = CASE WHEN d.last_signin_at < now() - interval '90 days' OR coalesce(v_stale, false) THEN NULL ELSE d.proved_at END,
        proved_by = CASE WHEN d.last_signin_at < now() - interval '90 days' OR coalesce(v_stale, false) THEN NULL ELSE d.proved_by END,
        last_signin_at = now()
  RETURNING d.proved_at INTO v_proved;
  RETURN v_proved IS NOT NULL;
END
$$;
-- +goose StatementEnd
