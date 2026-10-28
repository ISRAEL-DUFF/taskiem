-- When a session signed in, so that changing sign-in factors can ask a
-- person with none of their own (SSO only) to have signed in recently
-- (Atlas part D findings): taskiem_auth_session also returns created_at.

-- +goose Up
GRANT SELECT (created_at) ON sessions TO taskiem_dispatch;
DROP FUNCTION taskiem_auth_session(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_session(p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid, auth_method text, signed_in_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, s.tenant_id, s.auth_method, s.created_at FROM sessions s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
    JOIN users u ON u.id = s.user_id AND u.status = 'active'
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_session(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_session(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_session(bytea) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_session(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_session(p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid, auth_method text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, s.tenant_id, s.auth_method FROM sessions s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
    JOIN users u ON u.id = s.user_id AND u.status = 'active'
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_session(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_session(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_session(bytea) TO taskiem_app;
REVOKE SELECT (created_at) ON sessions FROM taskiem_dispatch;
