-- The public connector catalogue, part 3: browsing and installing (Phase 4
-- P4-6, decision 0020). Any tenant reads published versions through the
-- functions below (never the catalogue table itself) and installs one
-- version per major, pinned: new runs use exactly that version until the
-- tenant upgrades, and an upgrade that adds hosts or writes, or changes a
-- class, needs the tenant's consent again. A revoked version stops
-- loading at once for every tenant that installed it.

-- +goose Up
CREATE TABLE catalogue_installs (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  connector_id text NOT NULL CHECK (connector_id ~ '^p_[a-z][a-z0-9]{1,29}_[a-z][a-z0-9_]*$'),
  major        int NOT NULL CHECK (major >= 0),
  version      text NOT NULL CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$' AND split_part(version, '.', 1)::int = major),
  consent      jsonb NOT NULL,   -- the hosts and write classes the tenant agreed to
  installed_by text NOT NULL,
  installed_at timestamptz NOT NULL DEFAULT now(),
  updated_by   text,
  updated_at   timestamptz,
  PRIMARY KEY (tenant_id, connector_id, major)
);
ALTER TABLE catalogue_installs ENABLE ROW LEVEL SECURITY;
ALTER TABLE catalogue_installs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON catalogue_installs TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON catalogue_installs TO taskiem_app;
GRANT UPDATE (version, consent, updated_by, updated_at) ON catalogue_installs TO taskiem_app;
-- The catalogue's functions see which tenant pinned which version: routing
-- columns only.
CREATE POLICY dispatch ON catalogue_installs FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, connector_id, version) ON catalogue_installs TO taskiem_dispatch;

-- The catalogue: every published version, with its publisher's public
-- identity. Manifests of published versions are public; modules are not
-- returned here.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_listing()
RETURNS TABLE (connector_id text, version text, publisher text, publisher_name text, publisher_status text, manifest text, licence text,
               source_url text, package_digest text, published_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT v.connector_id, v.version, v.publisher, p.name, p.status, v.manifest, v.licence, v.source_url, encode(v.package_digest, 'hex'), v.published_at
    FROM catalogue_versions v JOIN connector_publishers p ON p.tenant_id = v.publisher_tenant
   WHERE v.state = 'published' AND p.status = 'verified'
   ORDER BY v.connector_id, v.published_at
$$;
-- +goose StatementEnd

-- One version that is or was in the catalogue (published or revoked), for
-- details and upgrade diffs.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_version(p_connector text, p_version text)
RETURNS TABLE (connector_id text, version text, publisher text, publisher_name text, state text, manifest text, licence text, source_url text,
               package_digest text, module_digest text, published_at timestamptz, revoked_at timestamptz, revoke_reason text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT v.connector_id, v.version, v.publisher, p.name, v.state, v.manifest, v.licence, v.source_url, encode(v.package_digest, 'hex'),
         encode(v.module_digest, 'hex'), v.published_at, v.revoked_at, v.revoke_reason
    FROM catalogue_versions v JOIN connector_publishers p ON p.tenant_id = v.publisher_tenant
   WHERE v.connector_id = p_connector AND v.version = p_version AND v.state IN ('published', 'revoked')
$$;
-- +goose StatementEnd

-- The versions a tenant installed that may run: published, from a
-- publisher that is not suspended. The tenant must be in scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_installed(p_tenant uuid)
RETURNS TABLE (connector_id text, version text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT i.connector_id, i.version
    FROM catalogue_installs i
    JOIN catalogue_versions v ON v.connector_id = i.connector_id AND v.version = i.version AND v.state = 'published'
    JOIN connector_publishers p ON p.tenant_id = v.publisher_tenant AND p.status = 'verified'
   WHERE i.tenant_id = p_tenant AND p_tenant = ANY (taskiem_tenant_scope())
$$;
-- +goose StatementEnd

-- One installed version's manifest and module, on the same conditions.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_module(p_tenant uuid, p_connector text, p_version text)
RETURNS TABLE (manifest text, module bytea, module_digest bytea)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT v.manifest, v.module, v.module_digest
    FROM catalogue_installs i
    JOIN catalogue_versions v ON v.connector_id = i.connector_id AND v.version = i.version AND v.state = 'published'
    JOIN connector_publishers p ON p.tenant_id = v.publisher_tenant AND p.status = 'verified'
   WHERE i.tenant_id = p_tenant AND i.connector_id = p_connector AND i.version = p_version AND p_tenant = ANY (taskiem_tenant_scope())
$$;
-- +goose StatementEnd

-- The tenants that installed a version: who to alert when it is revoked.
-- Tenant ids only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_installers(p_connector text, p_version text)
RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM catalogue_installs WHERE connector_id = p_connector AND version = p_version
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_catalogue_listing() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_version(text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_installed(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_module(uuid, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_catalogue_installers(text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_catalogue_listing(), taskiem_catalogue_version(text, text), taskiem_catalogue_installed(uuid),
  taskiem_catalogue_module(uuid, text, text), taskiem_catalogue_installers(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_catalogue_listing(), taskiem_catalogue_version(text, text), taskiem_catalogue_installed(uuid),
  taskiem_catalogue_module(uuid, text, text), taskiem_catalogue_installers(text, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_catalogue_installers(text, text);
DROP FUNCTION taskiem_catalogue_module(uuid, text, text);
DROP FUNCTION taskiem_catalogue_installed(uuid);
DROP FUNCTION taskiem_catalogue_version(text, text);
DROP FUNCTION taskiem_catalogue_listing();
DROP TABLE catalogue_installs;
