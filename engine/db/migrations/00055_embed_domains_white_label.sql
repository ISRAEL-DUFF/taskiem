-- Embedding C2 (spec 13.4; docs/embedding.md): partner capabilities set by
-- operators (white-label, custom domains), an embed app's white-label flag,
-- and custom domains for embed apps, verified by a DNS TXT record (as SSO
-- domains are) before any request with that Host reaches the app.

-- +goose Up
ALTER TABLE partners ADD COLUMN capabilities text[] NOT NULL DEFAULT '{}'
  CHECK (capabilities <@ ARRAY['white_label', 'custom_domains']::text[]);

ALTER TABLE embed_apps ADD COLUMN white_label boolean NOT NULL DEFAULT false;
ALTER TABLE embed_apps ADD CONSTRAINT embed_apps_tenant_id_id_key UNIQUE (tenant_id, id);
GRANT SELECT (white_label) ON embed_apps TO taskiem_dispatch;

-- A custom domain for an embed app (automations.partner.com). A claim is
-- the partner's own; only a verified claim is exclusive, so an unverified
-- one cannot keep the domain's owner from claiming it.
CREATE TABLE embed_app_domains (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),           -- the partner
  domain      text NOT NULL CHECK (domain = lower(domain) AND length(domain) <= 253
                AND domain ~ '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$'),
  app_id      uuid NOT NULL,
  token       text NOT NULL,                                  -- expected in the _taskiem-verify.<domain> TXT record
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  verified_at timestamptz,
  PRIMARY KEY (tenant_id, domain),
  FOREIGN KEY (tenant_id, app_id) REFERENCES embed_apps (tenant_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX embed_app_domains_verified ON embed_app_domains (domain) WHERE verified_at IS NOT NULL;
ALTER TABLE embed_app_domains ENABLE ROW LEVEL SECURITY;
ALTER TABLE embed_app_domains FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON embed_app_domains TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON embed_app_domains FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON embed_app_domains TO taskiem_app;
GRANT SELECT ON embed_app_domains TO taskiem_dispatch;

-- Operators set a partner's capabilities (taskiem tenants partner
-- --capabilities). The tenant must be in scope and a partner.
-- +goose StatementBegin
CREATE FUNCTION taskiem_set_partner_capabilities(p_tenant uuid, p_caps text[], p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  UPDATE partners SET capabilities = (SELECT COALESCE(array_agg(DISTINCT c ORDER BY c), '{}') FROM unnest(p_caps) c),
         updated_by = p_by, updated_at = now()
   WHERE tenant_id = p_tenant;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'tenant % is not a partner', p_tenant USING ERRCODE = 'P0002';
  END IF;
END
$$;
-- +goose StatementEnd

-- The embed app a Host serves: a verified domain of an active app of an
-- active partner that still holds the custom_domains capability.
-- +goose StatementBegin
CREATE FUNCTION taskiem_embed_host_app(p_host text)
RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.id FROM embed_app_domains d
    JOIN embed_apps a ON a.id = d.app_id AND a.tenant_id = d.tenant_id AND a.status = 'active'
    JOIN tenants p ON p.id = a.tenant_id AND p.status = 'active'
    JOIN partners pr ON pr.tenant_id = p.id AND 'custom_domains' = ANY (pr.capabilities)
   WHERE d.domain = lower(p_host) AND d.verified_at IS NOT NULL
$$;
-- +goose StatementEnd

-- Whether messages sent on a tenant's behalf leave out the platform's
-- branding: a sub-tenant whose partner holds white_label and has an active
-- white-label app. The tenant must be in scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tenant_white_label(p_tenant uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p_tenant = ANY (taskiem_tenant_scope()) AND EXISTS (
    SELECT 1 FROM tenants s
      JOIN partners pr ON pr.tenant_id = s.parent_id AND 'white_label' = ANY (pr.capabilities)
      JOIN embed_apps a ON a.tenant_id = pr.tenant_id AND a.status = 'active' AND a.white_label
     WHERE s.id = p_tenant)
$$;
-- +goose StatementEnd

-- An app's origins now include its verified custom domains (served over
-- https); a token resolves with the app's effective white-label flag.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_embed_app_origins(p_app uuid)
RETURNS text[]
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.allowed_origins || COALESCE((SELECT array_agg('https://' || d.domain ORDER BY d.domain) FROM embed_app_domains d
                                         JOIN partners pr2 ON pr2.tenant_id = d.tenant_id AND 'custom_domains' = ANY (pr2.capabilities)
                                        WHERE d.app_id = a.id AND d.tenant_id = a.tenant_id AND d.verified_at IS NOT NULL), '{}')
    FROM embed_apps a
    JOIN tenants p ON p.id = a.tenant_id AND p.status = 'active'
    JOIN partners pr ON pr.tenant_id = p.id
   WHERE a.id = p_app AND a.status = 'active'
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_auth_end_user_token(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_end_user_token(p_hash bytea)
RETURNS TABLE (token_id uuid, tenant_id uuid, partner_id uuid, app_id uuid, end_user_id uuid, external_id text, permissions text[], origin text,
               allowed_origins text[], allowed_connectors text[], allowed_templates text[], app_permissions text[], headless boolean, branding jsonb,
               expires_at timestamptz, white_label boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.id, k.tenant_id, s.parent_id, k.app_id, k.end_user_id, u.external_id, k.permissions, k.origin,
         a.allowed_origins || COALESCE((SELECT array_agg('https://' || d.domain ORDER BY d.domain) FROM embed_app_domains d
                                         WHERE d.app_id = a.id AND d.tenant_id = a.tenant_id AND d.verified_at IS NOT NULL
                                           AND 'custom_domains' = ANY (pr.capabilities)), '{}'),
         a.allowed_connectors, a.allowed_templates, a.end_user_permissions, a.headless, a.branding, k.expires_at,
         a.white_label AND 'white_label' = ANY (pr.capabilities)
    FROM end_user_tokens k
    JOIN tenants s ON s.id = k.tenant_id AND s.status = 'active'
    JOIN tenants p ON p.id = s.parent_id AND p.status = 'active'
    JOIN partners pr ON pr.tenant_id = p.id
    JOIN embed_apps a ON a.id = k.app_id AND a.tenant_id = p.id AND a.status = 'active'
    JOIN end_users u ON u.id = k.end_user_id AND u.tenant_id = k.tenant_id AND u.app_id = k.app_id
   WHERE k.token_hash = p_hash AND k.revoked_at IS NULL AND k.expires_at > now()
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_end_user_token(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_set_partner_capabilities(uuid, text[], text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_embed_host_app(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tenant_white_label(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_end_user_token(bytea), taskiem_set_partner_capabilities(uuid, text[], text),
  taskiem_embed_host_app(text), taskiem_tenant_white_label(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_end_user_token(bytea), taskiem_set_partner_capabilities(uuid, text[], text),
  taskiem_embed_host_app(text), taskiem_tenant_white_label(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_end_user_token(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_end_user_token(p_hash bytea)
RETURNS TABLE (token_id uuid, tenant_id uuid, partner_id uuid, app_id uuid, end_user_id uuid, external_id text, permissions text[], origin text,
               allowed_origins text[], allowed_connectors text[], allowed_templates text[], app_permissions text[], headless boolean, branding jsonb,
               expires_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.id, k.tenant_id, s.parent_id, k.app_id, k.end_user_id, u.external_id, k.permissions, k.origin,
         a.allowed_origins, a.allowed_connectors, a.allowed_templates, a.end_user_permissions, a.headless, a.branding, k.expires_at
    FROM end_user_tokens k
    JOIN tenants s ON s.id = k.tenant_id AND s.status = 'active'
    JOIN tenants p ON p.id = s.parent_id AND p.status = 'active'
    JOIN partners pr ON pr.tenant_id = p.id
    JOIN embed_apps a ON a.id = k.app_id AND a.tenant_id = p.id AND a.status = 'active'
    JOIN end_users u ON u.id = k.end_user_id AND u.tenant_id = k.tenant_id AND u.app_id = k.app_id
   WHERE k.token_hash = p_hash AND k.revoked_at IS NULL AND k.expires_at > now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_end_user_token(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_end_user_token(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_end_user_token(bytea) TO taskiem_app;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_embed_app_origins(p_app uuid)
RETURNS text[]
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.allowed_origins FROM embed_apps a
    JOIN tenants p ON p.id = a.tenant_id AND p.status = 'active'
    JOIN partners pr ON pr.tenant_id = p.id
   WHERE a.id = p_app AND a.status = 'active'
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_tenant_white_label(uuid);
DROP FUNCTION taskiem_embed_host_app(text);
DROP FUNCTION taskiem_set_partner_capabilities(uuid, text[], text);
DROP TABLE embed_app_domains;
REVOKE SELECT (white_label) ON embed_apps FROM taskiem_dispatch;
ALTER TABLE embed_apps DROP CONSTRAINT embed_apps_tenant_id_id_key;
ALTER TABLE embed_apps DROP COLUMN white_label;
ALTER TABLE partners DROP COLUMN capabilities;
