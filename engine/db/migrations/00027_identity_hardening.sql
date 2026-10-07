-- Identity hardening: SSO domains bound to their own tenant's connections,
-- SSO sign-ins bound to the browser that started them, invitations for
-- people who already have an account, a TOTP lockout, and no sign-in for
-- suspended tenants or disabled users.

-- +goose Up
-- A domain hangs off a connection of its own tenant. Foreign keys do not
-- see row-level security, so a key on connection_id alone let one tenant
-- attach a domain to another's connection.
DELETE FROM sso_domains d USING sso_connections c WHERE c.id = d.connection_id AND c.tenant_id <> d.tenant_id;
ALTER TABLE sso_connections ADD CONSTRAINT sso_connections_tenant_id_id_key UNIQUE (tenant_id, id);
ALTER TABLE sso_domains DROP CONSTRAINT sso_domains_connection_id_fkey;
ALTER TABLE sso_domains ADD CONSTRAINT sso_domains_connection_fkey
  FOREIGN KEY (tenant_id, connection_id) REFERENCES sso_connections (tenant_id, id) ON DELETE CASCADE;

-- A claim is one tenant's; only a verified claim is exclusive, so an
-- unverified one cannot keep the domain's owner from claiming it.
ALTER TABLE sso_domains DROP CONSTRAINT sso_domains_pkey;
ALTER TABLE sso_domains ADD PRIMARY KEY (tenant_id, domain);
CREATE UNIQUE INDEX sso_domains_verified ON sso_domains (domain) WHERE verified_at IS NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_sso_for_email(p_email text)
RETURNS TABLE (connection_id uuid, tenant_id uuid, enforce boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.id, c.tenant_id, c.enforce FROM sso_domains d JOIN sso_connections c ON c.id = d.connection_id AND c.tenant_id = d.tenant_id
   WHERE d.domain = lower(split_part(p_email, '@', 2)) AND d.verified_at IS NOT NULL AND c.disabled_at IS NULL
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_sso_connection(p_id uuid)
RETURNS TABLE (tenant_id uuid, protocol text, config jsonb, domains text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.protocol, c.config,
         COALESCE((SELECT array_agg(d.domain) FROM sso_domains d
                    WHERE d.connection_id = c.id AND d.tenant_id = c.tenant_id AND d.verified_at IS NOT NULL), '{}')
    FROM sso_connections c WHERE c.id = p_id AND c.disabled_at IS NULL
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_sso_enforced(p_user uuid, p_tenant uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM users u JOIN sso_domains d ON d.domain = lower(split_part(u.email, '@', 2))
                   JOIN sso_connections c ON c.id = d.connection_id AND c.tenant_id = d.tenant_id
                  WHERE u.id = p_user AND d.tenant_id = p_tenant AND d.verified_at IS NOT NULL AND c.enforce AND c.disabled_at IS NULL)
     AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = p_user AND m.tenant_id = p_tenant AND m.role = 'owner')
$$;
-- +goose StatementEnd

-- An SSO sign-in finishes only in the browser that started it: start sets
-- a cookie, and the request keeps its hash.
ALTER TABLE sso_requests ADD COLUMN binding bytea;

DROP FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_sso_begin(p_state bytea, p_connection uuid, p_tenant uuid, p_nonce text, p_verifier text, p_return text, p_binding bytea)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM sso_requests WHERE expires_at < now() - interval '1 hour';
  INSERT INTO sso_requests (state, connection_id, tenant_id, nonce, verifier, return_to, expires_at, binding)
  VALUES (p_state, p_connection, p_tenant, p_nonce, p_verifier, p_return, now() + interval '10 minutes', p_binding);
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_auth_sso_take(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_sso_take(p_state bytea)
RETURNS TABLE (connection_id uuid, tenant_id uuid, nonce text, verifier text, return_to text, binding bytea)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM sso_requests r WHERE r.state = p_state AND r.expires_at > now()
  RETURNING r.connection_id, r.tenant_id, r.nonce, r.verifier, r.return_to, r.binding;
$$;
-- +goose StatementEnd

-- Someone who already has an account joins another tenant only when they
-- accept. Until then the invitation grants nothing: it is not a membership.
CREATE TABLE member_invitations (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  roles       text[] NOT NULL DEFAULT '{}',
  source      text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'scim')),
  invited_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id)
);
CREATE INDEX ON member_invitations (user_id);
ALTER TABLE member_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE member_invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON member_invitations TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON member_invitations TO taskiem_app;
CREATE POLICY dispatch ON member_invitations FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, user_id) ON member_invitations TO taskiem_dispatch;

-- The active tenants inviting a person, for their own list.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_invited_tenants(p_user uuid)
RETURNS uuid[] LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(array_agg(i.tenant_id ORDER BY i.tenant_id), '{}') FROM member_invitations i
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE i.user_id = p_user
$$;
-- +goose StatementEnd

-- Whether a person belongs to any tenant but this one: a tenant may reset
-- only the passkeys of people who are wholly its own.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_member_elsewhere(p_user uuid, p_tenant uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = p_user AND m.tenant_id <> p_tenant)
$$;
-- +goose StatementEnd

-- TOTP guesses: after five wrong codes in fifteen minutes, codes are
-- refused for fifteen minutes.
ALTER TABLE member_mfa ADD COLUMN totp_failures int NOT NULL DEFAULT 0,
  ADD COLUMN totp_failed_since timestamptz, ADD COLUMN totp_locked_until timestamptz;

-- Sessions and keys stop working when their tenant is suspended or their
-- user disabled.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_session(p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid, auth_method text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, s.tenant_id, s.auth_method FROM sessions s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
    JOIN users u ON u.id = s.user_id AND u.status = 'active'
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_api_key(p_hash bytea)
RETURNS TABLE (key_id uuid, tenant_id uuid, permissions text[], environment text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.id, k.tenant_id, k.permissions, k.environment FROM api_keys k
    JOIN tenants t ON t.id = k.tenant_id AND t.status = 'active'
   WHERE k.key_hash = p_hash AND k.revoked_at IS NULL AND k.expires_at > now()
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_sso_take(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_invited_tenants(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_member_elsewhere(uuid, uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text, bytea), taskiem_auth_sso_take(bytea),
  taskiem_auth_invited_tenants(uuid), taskiem_auth_member_elsewhere(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text, bytea), taskiem_auth_sso_take(bytea),
  taskiem_auth_invited_tenants(uuid), taskiem_auth_member_elsewhere(uuid, uuid) TO taskiem_app;

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_api_key(p_hash bytea)
RETURNS TABLE (key_id uuid, tenant_id uuid, permissions text[], environment text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.id, k.tenant_id, k.permissions, k.environment FROM api_keys k
   WHERE k.key_hash = p_hash AND k.revoked_at IS NULL AND k.expires_at > now()
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_session(p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid, auth_method text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, s.tenant_id, s.auth_method FROM sessions s
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd

ALTER TABLE member_mfa DROP COLUMN totp_failures, DROP COLUMN totp_failed_since, DROP COLUMN totp_locked_until;
DROP FUNCTION taskiem_auth_member_elsewhere(uuid, uuid);
DROP FUNCTION taskiem_auth_invited_tenants(uuid);
DROP TABLE member_invitations;

DROP FUNCTION taskiem_auth_sso_take(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_sso_take(p_state bytea)
RETURNS TABLE (connection_id uuid, tenant_id uuid, nonce text, verifier text, return_to text)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM sso_requests r WHERE r.state = p_state AND r.expires_at > now()
  RETURNING r.connection_id, r.tenant_id, r.nonce, r.verifier, r.return_to;
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_sso_begin(p_state bytea, p_connection uuid, p_tenant uuid, p_nonce text, p_verifier text, p_return text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM sso_requests WHERE expires_at < now() - interval '1 hour';
  INSERT INTO sso_requests (state, connection_id, tenant_id, nonce, verifier, return_to, expires_at)
  VALUES (p_state, p_connection, p_tenant, p_nonce, p_verifier, p_return, now() + interval '10 minutes');
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_sso_take(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text), taskiem_auth_sso_take(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_sso_begin(bytea, uuid, uuid, text, text, text), taskiem_auth_sso_take(bytea) TO taskiem_app;
ALTER TABLE sso_requests DROP COLUMN binding;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_sso_enforced(p_user uuid, p_tenant uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM users u JOIN sso_domains d ON d.domain = lower(split_part(u.email, '@', 2))
                   JOIN sso_connections c ON c.id = d.connection_id
                  WHERE u.id = p_user AND d.tenant_id = p_tenant AND d.verified_at IS NOT NULL AND c.enforce AND c.disabled_at IS NULL)
     AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = p_user AND m.tenant_id = p_tenant AND m.role = 'owner')
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_sso_connection(p_id uuid)
RETURNS TABLE (tenant_id uuid, protocol text, config jsonb, domains text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.protocol, c.config,
         COALESCE((SELECT array_agg(d.domain) FROM sso_domains d WHERE d.connection_id = c.id AND d.verified_at IS NOT NULL), '{}')
    FROM sso_connections c WHERE c.id = p_id AND c.disabled_at IS NULL
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_sso_for_email(p_email text)
RETURNS TABLE (connection_id uuid, tenant_id uuid, enforce boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.id, c.tenant_id, c.enforce FROM sso_domains d JOIN sso_connections c ON c.id = d.connection_id
   WHERE d.domain = lower(split_part(p_email, '@', 2)) AND d.verified_at IS NOT NULL AND c.disabled_at IS NULL
$$;
-- +goose StatementEnd

-- Fails if two tenants now hold unverified claims to one domain.
DROP INDEX sso_domains_verified;
ALTER TABLE sso_domains DROP CONSTRAINT sso_domains_pkey;
ALTER TABLE sso_domains ADD PRIMARY KEY (domain);
ALTER TABLE sso_domains DROP CONSTRAINT sso_domains_connection_fkey;
ALTER TABLE sso_domains ADD CONSTRAINT sso_domains_connection_id_fkey FOREIGN KEY (connection_id) REFERENCES sso_connections (id) ON DELETE CASCADE;
ALTER TABLE sso_connections DROP CONSTRAINT sso_connections_tenant_id_id_key;
