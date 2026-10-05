-- Identity and tenancy. Spec 5.1, 13.1.

-- +goose Up
CREATE TABLE tenants (
  id          uuid PRIMARY KEY,
  parent_id   uuid REFERENCES tenants(id),         -- set for embedded sub-tenants
  name        text NOT NULL,
  region      text NOT NULL DEFAULT 'ng-lagos',
  plan_id     uuid NOT NULL,                       -- FK to plans added by the billing migration
  status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleted')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  CHECK (parent_id IS DISTINCT FROM id)
);
CREATE INDEX ON tenants (parent_id) WHERE parent_id IS NOT NULL;

-- Users are global identities; they reach tenants through memberships.
CREATE TABLE users (
  id             uuid PRIMARY KEY,
  email          text NOT NULL,
  name           text NOT NULL DEFAULT '',
  password_hash  text,                             -- argon2id; NULL for passkey/SSO-only users
  status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE memberships (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  role        text NOT NULL,                       -- owner | admin | builder | operator | approver | auditor | viewer | custom:<id>
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id, role)
);
CREATE INDEX ON memberships (user_id);

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenants TO taskiem_app
  USING (id = ANY (taskiem_tenant_scope())) WITH CHECK (id = ANY (taskiem_tenant_scope()));

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON memberships TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

-- A user row is visible when the user belongs to a tenant in scope.
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON users TO taskiem_app
  USING (EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = users.id AND m.tenant_id = ANY (taskiem_tenant_scope())));
-- Signup and invitation create users before any membership exists.
CREATE POLICY user_insert ON users FOR INSERT TO taskiem_app WITH CHECK (true);

GRANT SELECT, INSERT, UPDATE ON tenants, users TO taskiem_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON memberships TO taskiem_app;
