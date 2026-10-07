-- Password reset: single-use links sent by email, and ending a person's
-- sessions in every tenant when their password changes.

-- +goose Up
-- A reset link carries a selector, to find the row, and a 256-bit secret,
-- kept only as its SHA-256. A link works once, for thirty minutes, and not
-- after five wrong secrets for its selector. Asking again replaces any
-- unused link. Rows are a person's, not a tenant's: only the functions
-- below reach them.
CREATE TABLE password_resets (
  selector     bytea PRIMARY KEY CHECK (length(selector) = 16),
  secret_hash  bytea NOT NULL CHECK (length(secret_hash) = 32),
  user_id      uuid NOT NULL REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  used_at      timestamptz,
  failures     int NOT NULL DEFAULT 0,
  ip           text
);
CREATE INDEX ON password_resets (user_id);
ALTER TABLE password_resets ENABLE ROW LEVEL SECURITY;
ALTER TABLE password_resets FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON password_resets TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON password_resets TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON password_resets FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON password_resets TO taskiem_dispatch;

-- Issues a link, replacing the person's unused ones.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_password_reset_issue(p_user uuid, p_selector bytea, p_hash bytea, p_ttl interval, p_ip text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM password_resets WHERE expires_at < now() - interval '1 day';
  DELETE FROM password_resets WHERE user_id = p_user AND used_at IS NULL;
  INSERT INTO password_resets (selector, secret_hash, user_id, expires_at, ip) VALUES (p_selector, p_hash, p_user, now() + p_ttl, p_ip);
$$;
-- +goose StatementEnd

-- Checks a link without using it: ok (with the person), invalid (unknown,
-- used, expired or locked) or wrong (a wrong secret, which counts toward
-- the lock).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_password_reset_check(p_selector bytea, p_hash bytea)
RETURNS TABLE (user_id uuid, outcome text)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r password_resets;
BEGIN
  SELECT * INTO r FROM password_resets pr WHERE pr.selector = p_selector FOR UPDATE;
  IF NOT FOUND OR r.used_at IS NOT NULL OR r.expires_at <= now() OR r.failures >= 5 THEN
    RETURN QUERY SELECT NULL::uuid, 'invalid'::text;
    RETURN;
  END IF;
  -- Digests of a 256-bit secret: comparing them in variable time tells
  -- nothing about the secret.
  IF r.secret_hash <> p_hash THEN
    UPDATE password_resets pr SET failures = pr.failures + 1 WHERE pr.selector = p_selector;
    RETURN QUERY SELECT NULL::uuid, 'wrong'::text;
    RETURN;
  END IF;
  RETURN QUERY SELECT r.user_id, 'ok'::text;
END
$$;
-- +goose StatementEnd

-- Uses a link: at most once, in the transaction that sets the password.
-- The person's other unused links go with it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_password_reset_use(p_selector bytea, p_hash bytea)
RETURNS uuid LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM password_resets o USING password_resets pr
   WHERE pr.selector = p_selector AND o.user_id = pr.user_id AND o.selector <> p_selector AND o.used_at IS NULL;
  UPDATE password_resets pr SET used_at = now()
   WHERE pr.selector = p_selector AND pr.secret_hash = p_hash AND pr.used_at IS NULL AND pr.expires_at > now() AND pr.failures < 5
  RETURNING pr.user_id;
$$;
-- +goose StatementEnd

-- Ends a person's sessions in every tenant, suspended ones included, but
-- the one given (the session changing its own password).
CREATE POLICY dispatch_revoke ON sessions FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT UPDATE (revoked_at) ON sessions TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_revoke_sessions(p_user uuid, p_keep bytea)
RETURNS bigint LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH r AS (UPDATE sessions s SET revoked_at = now()
              WHERE s.user_id = p_user AND s.revoked_at IS NULL AND s.token_hash IS DISTINCT FROM p_keep RETURNING 1)
  SELECT count(*) FROM r
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_password_reset_issue(uuid, bytea, bytea, interval, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_password_reset_check(bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_password_reset_use(bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_revoke_sessions(uuid, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_password_reset_issue(uuid, bytea, bytea, interval, text), taskiem_auth_password_reset_check(bytea, bytea),
  taskiem_auth_password_reset_use(bytea, bytea), taskiem_auth_revoke_sessions(uuid, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_password_reset_issue(uuid, bytea, bytea, interval, text), taskiem_auth_password_reset_check(bytea, bytea),
  taskiem_auth_password_reset_use(bytea, bytea), taskiem_auth_revoke_sessions(uuid, bytea) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_revoke_sessions(uuid, bytea);
REVOKE UPDATE (revoked_at) ON sessions FROM taskiem_dispatch;
DROP POLICY dispatch_revoke ON sessions;
DROP FUNCTION taskiem_auth_password_reset_use(bytea, bytea);
DROP FUNCTION taskiem_auth_password_reset_check(bytea, bytea);
DROP FUNCTION taskiem_auth_password_reset_issue(uuid, bytea, bytea, interval, text);
DROP TABLE password_resets;
