-- Single sign-on (spec 13.2): a tenant's OIDC or SAML identity provider.

-- +goose Up
CREATE TABLE sso_connections (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  protocol      text NOT NULL CHECK (protocol IN ('oidc', 'saml')),
  name          text NOT NULL,
  config        jsonb NOT NULL,           -- protocol settings; secrets live in the vault
  default_roles text[] NOT NULL DEFAULT '{}',
  group_roles   jsonb NOT NULL DEFAULT '{}', -- IdP group -> roles
  jit           boolean NOT NULL DEFAULT true,  -- create members on first sign-in
  enforce       boolean NOT NULL DEFAULT false, -- members on its domains must use it
  created_by    text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  disabled_at   timestamptz
);

-- Email domains a connection serves, each proven with a DNS TXT record
-- before it routes anyone: otherwise a tenant could claim someone else's
-- domain and send its people to a fake identity provider.
CREATE TABLE sso_domains (
  domain        text PRIMARY KEY CHECK (domain = lower(domain) AND domain ~ '^[a-z0-9.-]+\.[a-z]{2,}$'),
  connection_id uuid NOT NULL REFERENCES sso_connections(id) ON DELETE CASCADE,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  token         text NOT NULL,             -- expected in _taskiem-verify.<domain> TXT
  verified_at   timestamptz
);

-- Sign-ins in flight: state, nonce and PKCE verifier, single use.
CREATE TABLE sso_requests (
  state         bytea PRIMARY KEY CHECK (length(state) = 32),
  connection_id uuid NOT NULL,
  tenant_id     uuid NOT NULL,
  nonce         text NOT NULL,
  verifier      text NOT NULL,             -- PKCE code verifier, or the SAML request id
  return_to     text NOT NULL DEFAULT '/',
  expires_at    timestamptz NOT NULL
);

-- Where a membership came from: SSO and SCIM manage only their own.
ALTER TABLE memberships ADD COLUMN source text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'sso', 'scim'));

ALTER TABLE sso_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE sso_connections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sso_connections TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON sso_connections TO taskiem_app;
CREATE POLICY dispatch ON sso_connections FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON sso_connections TO taskiem_dispatch;

ALTER TABLE sso_domains ENABLE ROW LEVEL SECURITY;
ALTER TABLE sso_domains FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sso_domains TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON sso_domains TO taskiem_app;
CREATE POLICY dispatch ON sso_domains FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON sso_domains TO taskiem_dispatch;

ALTER TABLE sso_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE sso_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON sso_requests TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON sso_requests TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON sso_requests FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON sso_requests TO taskiem_dispatch;
GRANT SELECT (source) ON memberships TO taskiem_dispatch;

-- The connection serving an email's verified domain, before sign-in.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_sso_for_email(p_email text)
RETURNS TABLE (connection_id uuid, tenant_id uuid, enforce boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.id, c.tenant_id, c.enforce FROM sso_domains d JOIN sso_connections c ON c.id = d.connection_id
   WHERE d.domain = lower(split_part(p_email, '@', 2)) AND d.verified_at IS NOT NULL AND c.disabled_at IS NULL
$$;
-- +goose StatementEnd

-- A connection by id, before sign-in.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_sso_connection(p_id uuid)
RETURNS TABLE (tenant_id uuid, protocol text, config jsonb, domains text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.protocol, c.config,
         COALESCE((SELECT array_agg(d.domain) FROM sso_domains d WHERE d.connection_id = c.id AND d.verified_at IS NOT NULL), '{}')
    FROM sso_connections c WHERE c.id = p_id AND c.disabled_at IS NULL
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_sso_begin(p_state bytea, p_connection uuid, p_tenant uuid, p_nonce text, p_verifier text, p_return text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM sso_requests WHERE expires_at < now() - interval '1 hour';
  INSERT INTO sso_requests (state, connection_id, tenant_id, nonce, verifier, return_to, expires_at)
  VALUES (p_state, p_connection, p_tenant, p_nonce, p_verifier, p_return, now() + interval '10 minutes');
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_sso_take(p_state bytea)
RETURNS TABLE (connection_id uuid, tenant_id uuid, nonce text, verifier text, return_to text)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM sso_requests r WHERE r.state = p_state AND r.expires_at > now()
  RETURNING r.connection_id, r.tenant_id, r.nonce, r.verifier, r.return_to;
$$;
-- +goose StatementEnd

-- Whether a tenant requires its SSO for this person: their email is on a
-- verified domain of an enforcing connection of the tenant. Owners are
-- exempt, so a broken identity provider cannot lock the tenant out.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_sso_enforced(p_user uuid, p_tenant uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM users u JOIN sso_domains d ON d.domain = lower(split_part(u.email, '@', 2))
                   JOIN sso_connections c ON c.id = d.connection_id
                  WHERE u.id = p_user AND d.tenant_id = p_tenant AND d.verified_at IS NOT NULL AND c.enforce AND c.disabled_at IS NULL)
     AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = p_user AND m.tenant_id = p_tenant AND m.role = 'owner')
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_sso_enforced(uuid, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_sso_for_email(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_sso_connection(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_sso_take(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_sso_enforced(uuid, uuid), taskiem_auth_sso_for_email(text), taskiem_auth_sso_connection(uuid),
  taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text), taskiem_auth_sso_take(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_sso_enforced(uuid, uuid), taskiem_auth_sso_for_email(text), taskiem_auth_sso_connection(uuid),
  taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text), taskiem_auth_sso_take(bytea) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_sso_enforced(uuid, uuid);
DROP FUNCTION taskiem_auth_sso_take(bytea);
DROP FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text);
DROP FUNCTION taskiem_auth_sso_connection(uuid);
DROP FUNCTION taskiem_auth_sso_for_email(text);
ALTER TABLE memberships DROP COLUMN source;
DROP TABLE sso_requests;
DROP TABLE sso_domains;
DROP TABLE sso_connections;
