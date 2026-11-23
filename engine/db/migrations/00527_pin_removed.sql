-- A removed PIN is remembered (that one was removed, and when; nothing of
-- the PIN), so the next one is not a "first" PIN: setting it again takes
-- what changing it takes (a passkey or an authenticator, or a sign-in
-- moments ago), and a session someone took cannot remove the PIN and set
-- one of their own as if none had been set.
-- +goose Up
CREATE TABLE pin_removals (
  user_id    uuid PRIMARY KEY REFERENCES users(id),
  removed_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE pin_removals ENABLE ROW LEVEL SECURITY;
ALTER TABLE pin_removals FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON pin_removals TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON pin_removals TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON pin_removals FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON pin_removals TO taskiem_dispatch;

-- Removes a person's PIN, remembering that it was; true when there was one.
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

-- Whether a person ever removed a PIN.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pin_was_removed(p_user uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM pin_removals r WHERE r.user_id = p_user)
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_pin_was_removed(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_pin_was_removed(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_pin_was_removed(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_pin_was_removed(uuid);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_pin_remove(p_user uuid)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH d AS (DELETE FROM whatsapp_pins p WHERE p.user_id = p_user RETURNING 1) SELECT EXISTS (SELECT 1 FROM d)
$$;
-- +goose StatementEnd
DROP TABLE pin_removals;
