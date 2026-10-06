-- Custom roles (spec 13.3): a tenant's own named sets of permissions.
-- A membership names a role: built-in (owner, admin, ...), custom (a row
-- here), or otherwise a business role that only qualifies the member for
-- approval steps naming it.

-- +goose Up
CREATE TABLE roles (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  name        text NOT NULL CHECK (name ~ '^[a-z][a-z0-9_]{1,47}$'),
  description text NOT NULL DEFAULT '',
  permissions text[] NOT NULL,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, name)
);

ALTER TABLE roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE roles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON roles TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON roles TO taskiem_app;

-- +goose Down
DROP TABLE roles;
