-- OAuth connections (decision 0034; docs/connector-sdk.md#connecting-by-oauth).
--
-- A person links a connector by OAuth 2.0 authorization code grant: Taskiem
-- sends their browser to the provider's consent page and the provider sends
-- it back to /v1/connections/oauth/callback with a code. The request in
-- flight lives here for ten minutes: which tenant, person, connector,
-- environment and name it is for, the state the browser carries back
-- (hashed at rest: the row cannot be found from a database read alone),
-- and the PKCE code verifier, sealed under the tenant's key like a
-- connection's credentials (the verifier is what proves to the provider
-- that the exchange comes from whoever started the request).
--
-- The callback is unauthenticated (the browser arrives from the provider),
-- so the state alone names the request: taskiem_oauth_request_take finds
-- the row without a tenant scope, as taskiem_auth_sso_take does for
-- sign-ins, and marks it used in the same statement so a state works once.
-- Starting a request happens in the person's own tenant transaction.

-- +goose Up
CREATE TABLE oauth_connection_requests (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  state_hash    bytea NOT NULL UNIQUE CHECK (length(state_hash) = 32),  -- SHA-256 of the state the browser carries
  person        text NOT NULL,                                          -- the actor who started it
  connector     text NOT NULL,                                          -- id@major, e.g. pgdock@1
  environment   text NOT NULL,
  name          text NOT NULL,
  verifier      bytea NOT NULL,                                         -- PKCE code verifier, sealed
  verifier_key  bytea NOT NULL,                                         -- its wrapped data key
  kek_version   int NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  used_at       timestamptz
);
ALTER TABLE oauth_connection_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_connection_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON oauth_connection_requests TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON oauth_connection_requests TO taskiem_app;
CREATE POLICY dispatch ON oauth_connection_requests TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE, DELETE ON oauth_connection_requests TO taskiem_dispatch;

-- The request a state names, marked used: once, while it lasts.
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

-- +goose Down
DROP FUNCTION taskiem_oauth_request_take(bytea);
DROP TABLE oauth_connection_requests;
