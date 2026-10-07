-- Invitations (docs/governance.md#members-and-invitations): admins list
-- and cancel them, invitees decline them, and a person with an account but
-- no membership anywhere can sign in to see and answer theirs. Such a
-- sign-in is a person's, not a tenant's: an invitee session lives in its
-- own person-level table, reached only through these functions, and the
-- API lets it reach the invitation routes and nothing else.

-- +goose Up
CREATE TABLE invitee_sessions (
  token_hash   bytea PRIMARY KEY,
  user_id      uuid NOT NULL REFERENCES users(id),
  auth_method  text NOT NULL CHECK (auth_method IN ('password', 'passkey')),
  ip           text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  revoked_at   timestamptz
);
CREATE INDEX ON invitee_sessions (user_id);
ALTER TABLE invitee_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitee_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON invitee_sessions TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON invitee_sessions TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON invitee_sessions FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON invitee_sessions TO taskiem_dispatch;

-- Invitee sessions and invitation lists show the person's name.
GRANT SELECT (name) ON users TO taskiem_dispatch;

-- Starts an invitee session for an active person who belongs to no active
-- tenant and is invited by at least one; false otherwise.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_invitee_start(p_hash bytea, p_user uuid, p_method text, p_ttl interval, p_ip text)
RETURNS boolean
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM invitee_sessions WHERE expires_at < now() - interval '1 day';
  IF NOT EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.status = 'active')
     OR EXISTS (SELECT 1 FROM memberships m JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active' WHERE m.user_id = p_user)
     OR NOT EXISTS (SELECT 1 FROM member_invitations i JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active' WHERE i.user_id = p_user) THEN
    RETURN false;
  END IF;
  INSERT INTO invitee_sessions (token_hash, user_id, auth_method, ip, expires_at) VALUES (p_hash, p_user, p_method, p_ip, now() + p_ttl);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- The person behind a live invitee session.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_invitee_session(p_hash bytea)
RETURNS TABLE (user_id uuid, email text, name text, auth_method text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, u.email, u.name, s.auth_method FROM invitee_sessions s
    JOIN users u ON u.id = s.user_id AND u.status = 'active'
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_invitee_end(p_hash bytea)
RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE invitee_sessions SET revoked_at = now() WHERE token_hash = p_hash AND revoked_at IS NULL
$$;
-- +goose StatementEnd

-- Who a tenant in scope has invited: their email and name, which its
-- admins typed to invite them, for its list of pending invitations.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tenant_invitees(p_tenant uuid)
RETURNS TABLE (user_id uuid, email text, name text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.id, u.email, u.name FROM member_invitations i JOIN users u ON u.id = i.user_id
   WHERE p_tenant = ANY (taskiem_tenant_scope()) AND i.tenant_id = p_tenant
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_invitee_start(bytea, uuid, text, interval, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_invitee_session(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_invitee_end(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tenant_invitees(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_invitee_start(bytea, uuid, text, interval, text), taskiem_auth_invitee_session(bytea),
  taskiem_auth_invitee_end(bytea), taskiem_tenant_invitees(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_invitee_start(bytea, uuid, text, interval, text), taskiem_auth_invitee_session(bytea),
  taskiem_auth_invitee_end(bytea), taskiem_tenant_invitees(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_tenant_invitees(uuid);
DROP FUNCTION taskiem_auth_invitee_end(bytea);
DROP FUNCTION taskiem_auth_invitee_session(bytea);
DROP FUNCTION taskiem_auth_invitee_start(bytea, uuid, text, interval, text);
DROP TABLE invitee_sessions;
REVOKE SELECT (name) ON users FROM taskiem_dispatch;
