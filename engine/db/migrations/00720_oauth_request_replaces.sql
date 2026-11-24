-- Reconnecting a connection in one step (Payrolla, P4b-1 of
-- docs/spec/chat-pay-people.md; decision 0074): an owner whose Payrolla
-- connection holds a pasted key, or whose connection ended, connects again
-- through the provider's consent page, and the callback moves that same
-- connection onto the new tokens (its name, workflows and staff links
-- kept) instead of making a second one. The request keeps which
-- connection it replaces until the provider sends the browser back.

-- +goose Up
ALTER TABLE oauth_connection_requests ADD COLUMN replaces uuid;

DROP FUNCTION taskiem_oauth_request_take(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_oauth_request_take(p_state_hash bytea)
RETURNS TABLE (id uuid, tenant_id uuid, person text, connector text, environment text, name text, mode text, replaces uuid, verifier bytea, verifier_key bytea, kek_version int)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE oauth_connection_requests r SET used_at = now()
   WHERE r.state_hash = p_state_hash AND r.used_at IS NULL AND r.expires_at > now()
  RETURNING r.id, r.tenant_id, r.person, r.connector, r.environment, r.name, r.mode, r.replaces, r.verifier, r.verifier_key, r.kek_version;
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_oauth_request_take(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_oauth_request_take(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_oauth_request_take(bytea) TO taskiem_app;

-- +goose Down
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
ALTER TABLE oauth_connection_requests DROP COLUMN replaces;
