-- Embedding (spec 13.4 steps 1–2; decision 0015): a partner's embed apps,
-- its end users (lightweight principals inside a sub-tenant, no platform
-- login) and their short-lived tokens. Tokens are opaque and stored as
-- SHA-256 hashes, like sessions and API keys.

-- +goose Up
CREATE TABLE embed_apps (
  id                   uuid PRIMARY KEY,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),   -- the partner
  name                 text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  allowed_origins      text[] NOT NULL DEFAULT '{}',           -- exact origins: scheme://host[:port]
  branding             jsonb NOT NULL DEFAULT '{}',            -- validated theming tokens (C2 renders them)
  allowed_connectors   text[] NOT NULL DEFAULT '{}',           -- connector ids, plus http, code and ai for those step types
  allowed_templates    text[] NOT NULL DEFAULT '{}',
  end_user_permissions text[] NOT NULL DEFAULT '{workflow.read,workflow.edit,run.read,run.start}',
  headless             boolean NOT NULL DEFAULT false,         -- tokens usable without an Origin (server-side calls)
  webhook_url          text,
  webhook_events       text[] NOT NULL DEFAULT '{run.completed,run.failed,workflow.published,usage.threshold}',
  status               text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  created_by           text NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);

-- An end user of an embed app inside one sub-tenant. Its id is what
-- workflows' created_by and published_by record for it.
CREATE TABLE end_users (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),          -- the sub-tenant
  app_id        uuid NOT NULL REFERENCES embed_apps(id),
  external_id   text NOT NULL CHECK (external_id ~ '^[A-Za-z0-9._:@|-]{1,128}$'),
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_token_at timestamptz,
  UNIQUE (tenant_id, app_id, external_id)
);

CREATE TABLE end_user_tokens (
  token_hash   bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  id           uuid NOT NULL UNIQUE,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),           -- the sub-tenant
  app_id       uuid NOT NULL REFERENCES embed_apps(id),
  end_user_id  uuid NOT NULL REFERENCES end_users(id),
  permissions  text[] NOT NULL,
  origin       text,                                           -- bound origin; NULL: any of the app's
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  revoked_at   timestamptz,
  CHECK (expires_at > created_at AND expires_at <= created_at + interval '1 hour')
);
CREATE INDEX end_user_tokens_user ON end_user_tokens (end_user_id) WHERE revoked_at IS NULL;
CREATE INDEX end_user_tokens_expired ON end_user_tokens (expires_at);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['embed_apps', 'end_users', 'end_user_tokens'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('CREATE POLICY dispatch ON %I FOR SELECT TO taskiem_dispatch USING (true)', t);
  END LOOP;
END
$$;
-- +goose StatementEnd
GRANT SELECT, INSERT, UPDATE ON embed_apps, end_users TO taskiem_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON end_user_tokens TO taskiem_app;
GRANT SELECT (id, tenant_id, allowed_origins, branding, allowed_connectors, allowed_templates, end_user_permissions, headless, status, webhook_url, webhook_events)
  ON embed_apps TO taskiem_dispatch;
GRANT SELECT (id, tenant_id, app_id, external_id) ON end_users TO taskiem_dispatch;
GRANT SELECT (token_hash, id, tenant_id, app_id, end_user_id, permissions, origin, expires_at, revoked_at) ON end_user_tokens TO taskiem_dispatch;

-- Resolve an end-user token before any tenant scope exists. It works only
-- while the token is unexpired and unrevoked, its sub-tenant and partner
-- are active, the partner is still a partner, and the app is active.
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

-- An active app's allowed origins, for CORS preflights (which carry no
-- token). Origins are not secret: a browser sees them in every response.
-- +goose StatementBegin
CREATE FUNCTION taskiem_embed_app_origins(p_app uuid)
RETURNS text[]
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.allowed_origins FROM embed_apps a
    JOIN tenants p ON p.id = a.tenant_id AND p.status = 'active'
    JOIN partners pr ON pr.tenant_id = p.id
   WHERE a.id = p_app AND a.status = 'active'
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_end_user_token(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_embed_app_origins(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_end_user_token(bytea), taskiem_embed_app_origins(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_end_user_token(bytea), taskiem_embed_app_origins(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_embed_app_origins(uuid);
DROP FUNCTION taskiem_auth_end_user_token(bytea);
DROP TABLE end_user_tokens;
DROP TABLE end_users;
DROP TABLE embed_apps;
