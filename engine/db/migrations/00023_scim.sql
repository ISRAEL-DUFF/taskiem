-- SCIM 2.0 provisioning (spec 13.2): an identity provider creates, updates
-- and deactivates a tenant's members, and moves them between groups.

-- +goose Up
-- How the tenant maps provisioned people to roles. An administrator sets
-- it, held to the no-escalation rule; the SCIM key only moves people.
CREATE TABLE scim_config (
  tenant_id     uuid PRIMARY KEY REFERENCES tenants(id),
  default_roles text[] NOT NULL DEFAULT '{viewer}',
  group_roles   jsonb NOT NULL DEFAULT '{}',  -- group displayName -> roles
  updated_by    text NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now()
);

-- People as the identity provider sees them. The SCIM id is the user id.
-- A deactivated person keeps their row, so the provider can read and
-- reactivate them.
CREATE TABLE scim_users (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  user_id       uuid NOT NULL REFERENCES users(id),
  user_name     text NOT NULL,
  email         text NOT NULL,              -- the user's sign-in email, kept here
                                            -- because a deactivated person's user
                                            -- row is no longer visible to the tenant
  external_id   text,
  display_name  text NOT NULL DEFAULT '',
  given_name    text NOT NULL DEFAULT '',
  family_name   text NOT NULL DEFAULT '',
  active        boolean NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id)
);
CREATE UNIQUE INDEX scim_users_user_name ON scim_users (tenant_id, lower(user_name));

CREATE TABLE scim_groups (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  display_name  text NOT NULL,
  external_id   text,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX scim_groups_display_name ON scim_groups (tenant_id, lower(display_name));

CREATE TABLE scim_group_members (
  tenant_id     uuid NOT NULL,
  group_id      uuid NOT NULL REFERENCES scim_groups(id) ON DELETE CASCADE,
  user_id       uuid NOT NULL,
  PRIMARY KEY (group_id, user_id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES scim_users(tenant_id, user_id) ON DELETE CASCADE
);
CREATE INDEX ON scim_group_members (tenant_id, user_id);

ALTER TABLE scim_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE scim_config FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scim_config TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON scim_config TO taskiem_app;

ALTER TABLE scim_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE scim_users FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scim_users TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON scim_users TO taskiem_app;

ALTER TABLE scim_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE scim_groups FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scim_groups TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON scim_groups TO taskiem_app;

ALTER TABLE scim_group_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE scim_group_members FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON scim_group_members TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON scim_group_members TO taskiem_app;

-- +goose Down
DROP TABLE scim_group_members;
DROP TABLE scim_groups;
DROP TABLE scim_users;
DROP TABLE scim_config;
