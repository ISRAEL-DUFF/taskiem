-- Embedding C3, the partner connector bridge (spec 13.4 step 4;
-- docs/embedding.md): a partner shares its own WebAssembly connectors
-- (tenant_connectors) read-only into its sub-tenants, and provisions each
-- sub-tenant's credentials for them through the partner API, into the
-- sub-tenant's own vault.

-- +goose Up

-- A partner's connector shared with its sub-tenants: every enabled version
-- of the id, present and future. Sub-tenants read it through
-- taskiem_shared_connectors only; they never see the partner's rows.
CREATE TABLE shared_connectors (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),          -- the partner
  connector_id text NOT NULL CHECK (connector_id ~ '^x_[a-z0-9_]{1,61}$'),
  shared_by    text NOT NULL,
  shared_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, connector_id)
);
ALTER TABLE shared_connectors ENABLE ROW LEVEL SECURITY;
ALTER TABLE shared_connectors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shared_connectors TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON shared_connectors FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, DELETE ON shared_connectors TO taskiem_app;
GRANT SELECT ON shared_connectors TO taskiem_dispatch;

CREATE POLICY dispatch ON tenant_connectors FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, connector_id, version, manifest, module, digest, disabled_at) ON tenant_connectors TO taskiem_dispatch;

-- Who provisioned a connection from outside the tenant: the partner's key,
-- as partner:<partner>/key:<id>. NULL for the tenant's own connections.
ALTER TABLE connections ADD COLUMN provisioned_by text;

-- The connectors a sub-tenant's partner shares with it: the enabled
-- versions of each shared id, while the partner is an active partner. The
-- sub-tenant must be in scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_shared_connectors(p_tenant uuid)
RETURNS TABLE (partner_id uuid, connector_id text, version text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.connector_id, c.version
    FROM tenants s
    JOIN tenants p ON p.id = s.parent_id AND p.status = 'active'
    JOIN partners pr ON pr.tenant_id = p.id
    JOIN shared_connectors sc ON sc.tenant_id = p.id
    JOIN tenant_connectors c ON c.tenant_id = p.id AND c.connector_id = sc.connector_id AND c.disabled_at IS NULL
   WHERE s.id = p_tenant AND p_tenant = ANY (taskiem_tenant_scope())
$$;
-- +goose StatementEnd

-- One shared version's manifest and module, on the same conditions.
-- +goose StatementBegin
CREATE FUNCTION taskiem_shared_connector_module(p_tenant uuid, p_connector text, p_version text)
RETURNS TABLE (manifest text, module bytea)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.manifest, c.module
    FROM tenants s
    JOIN tenants p ON p.id = s.parent_id AND p.status = 'active'
    JOIN partners pr ON pr.tenant_id = p.id
    JOIN shared_connectors sc ON sc.tenant_id = p.id AND sc.connector_id = p_connector
    JOIN tenant_connectors c ON c.tenant_id = p.id AND c.connector_id = p_connector AND c.version = p_version AND c.disabled_at IS NULL
   WHERE s.id = p_tenant AND p_tenant = ANY (taskiem_tenant_scope())
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_shared_connectors(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_shared_connector_module(uuid, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_shared_connectors(uuid), taskiem_shared_connector_module(uuid, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_shared_connectors(uuid), taskiem_shared_connector_module(uuid, text, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_shared_connector_module(uuid, text, text);
DROP FUNCTION taskiem_shared_connectors(uuid);
ALTER TABLE connections DROP COLUMN provisioned_by;
REVOKE SELECT ON tenant_connectors FROM taskiem_dispatch;
DROP POLICY dispatch ON tenant_connectors;
DROP TABLE shared_connectors;
