-- Tenants' own connectors (spec 6): a connector/v1 manifest and a
-- WebAssembly module implementing it, run out of process by the engine.

-- +goose Up
CREATE TABLE tenant_connectors (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  connector_id text NOT NULL CHECK (connector_id ~ '^x_[a-z0-9_]{1,61}$'),
  version      text NOT NULL CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),
  manifest     text NOT NULL,
  module       bytea NOT NULL,
  digest       bytea NOT NULL CHECK (length(digest) = 32), -- SHA-256 of the module
  uploaded_by  text NOT NULL,
  uploaded_at  timestamptz NOT NULL DEFAULT now(),
  disabled_at  timestamptz,
  disabled_by  text,
  PRIMARY KEY (tenant_id, connector_id, version)
);

ALTER TABLE tenant_connectors ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_connectors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_connectors TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
-- A version is never changed or removed, only disabled: runs and audit
-- entries name it.
GRANT SELECT, INSERT ON tenant_connectors TO taskiem_app;
GRANT UPDATE (disabled_at, disabled_by) ON tenant_connectors TO taskiem_app;

-- +goose Down
DROP TABLE tenant_connectors;
