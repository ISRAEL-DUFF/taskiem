-- Device sessions: staying signed in on a phone (docs/spec/mobile-app.md
-- §5.2, S2). A session the phone app asks for names the phone it is on;
-- it lasts 30 days without use, renewed by use up to 180 days from signing
-- in, and is listed under Your phones to be ended from anywhere. Everything
-- that ends sessions today (signing out everywhere, a password changed, a
-- role ending) ends these too: they are sessions.
--
-- Renewal happens while authenticating, before any tenant scope exists, so
-- it is a definer: taskiem_auth_session_touch extends only a live device
-- session, by its token's hash, at most once an hour, and answers nothing.

-- +goose Up
ALTER TABLE sessions
  ADD COLUMN device_id       uuid,
  ADD COLUMN device_name     text CHECK (device_name IS NULL OR length(device_name) BETWEEN 1 AND 80),
  ADD COLUMN device_platform text CHECK (device_platform IS NULL OR device_platform IN ('android', 'ios')),
  ADD COLUMN last_used_at    timestamptz,
  ADD CONSTRAINT sessions_device_named CHECK ((device_id IS NULL) = (device_platform IS NULL));

CREATE INDEX sessions_device ON sessions (device_id) WHERE device_id IS NOT NULL;

GRANT SELECT (device_id, last_used_at) ON sessions TO taskiem_dispatch;
GRANT UPDATE (expires_at, last_used_at) ON sessions TO taskiem_dispatch;
CREATE POLICY dispatch_touch ON sessions FOR UPDATE TO taskiem_dispatch
  USING (device_id IS NOT NULL) WITH CHECK (device_id IS NOT NULL);

DROP FUNCTION taskiem_auth_session(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_session(p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid, auth_method text, signed_in_at timestamptz, device boolean, last_used_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, s.tenant_id, s.auth_method, s.created_at, s.device_id IS NOT NULL, s.last_used_at FROM sessions s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
    JOIN users u ON u.id = s.user_id AND u.status = 'active'
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_session(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_session(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_session(bytea) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_session_touch(p_hash bytea)
RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE sessions
     SET expires_at = least(now() + interval '30 days', created_at + interval '180 days'),
         last_used_at = now()
   WHERE token_hash = p_hash AND device_id IS NOT NULL
     AND revoked_at IS NULL AND expires_at > now()
     AND (last_used_at IS NULL OR last_used_at < now() - interval '1 hour')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_session_touch(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_session_touch(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_session_touch(bytea) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_session_touch(bytea);
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
DROP POLICY dispatch_touch ON sessions;
REVOKE UPDATE (expires_at, last_used_at) ON sessions FROM taskiem_dispatch;
REVOKE SELECT (device_id, last_used_at) ON sessions FROM taskiem_dispatch;
DROP INDEX sessions_device;
ALTER TABLE sessions DROP CONSTRAINT sessions_device_named,
  DROP COLUMN last_used_at, DROP COLUMN device_platform, DROP COLUMN device_name, DROP COLUMN device_id;
