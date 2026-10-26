-- The mode a connection made by OAuth is in (QuickBooks, K3 of
-- docs/spec/business-connectors.md; connector/v1 rule 20): the person
-- chooses live or test before going to the provider, and the request keeps
-- the choice until the provider sends the browser back. A mode may have its
-- own platform app (Intuit's development keys reach only sandbox
-- companies), so the callback needs it before the code is exchanged.

-- +goose Up
ALTER TABLE oauth_connection_requests ADD COLUMN mode text CHECK (mode IS NULL OR mode ~ '^[a-z][a-z0-9_]*$');

DROP FUNCTION taskiem_oauth_request_take(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_oauth_request_take(p_state_hash bytea)
RETURNS TABLE (id uuid, tenant_id uuid, person text, connector text, environment text, name text, mode text, verifier bytea, verifier_key bytea, kek_version int)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE oauth_connection_requests r SET used_at = now()
   WHERE r.state_hash = p_state_hash AND r.used_at IS NULL AND r.expires_at > now()
  RETURNING r.id, r.tenant_id, r.person, r.connector, r.environment, r.name, r.mode, r.verifier, r.verifier_key, r.kek_version;
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_oauth_request_take(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_oauth_request_take(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_oauth_request_take(bytea) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_oauth_request_take(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_oauth_request_take(p_state_hash bytea)
RETURNS TABLE (id uuid, tenant_id uuid, person text, connector text, environment text, name text, verifier bytea, verifier_key bytea, kek_version int)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE oauth_connection_requests r SET used_at = now()
   WHERE r.state_hash = p_state_hash AND r.used_at IS NULL AND r.expires_at > now()
  RETURNING r.id, r.tenant_id, r.person, r.connector, r.environment, r.name, r.verifier, r.verifier_key, r.kek_version;
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_oauth_request_take(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_oauth_request_take(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_oauth_request_take(bytea) TO taskiem_app;
ALTER TABLE oauth_connection_requests DROP COLUMN mode;
