-- Workflow packages (decision 0030; docs/embedding.md#14-workflow-packages).
--
-- A partner shares one of its own workflows as a package, and installs a
-- copy of one of its versions into a sub-tenant. The copy is the
-- sub-tenant's own workflow: its runs, secrets and variables are the
-- sub-tenant's, and publishing it follows the sub-tenant's governance.
-- package_installs remembers which package a sub-tenant's workflow came
-- from, so installing a newer version makes a new draft of the same
-- workflow rather than a second workflow.

-- +goose Up
CREATE TABLE shared_workflows (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),          -- the partner
  workflow_id  uuid NOT NULL,
  shared_by    text NOT NULL,
  shared_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, workflow_id),
  FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflows (tenant_id, id)
);
ALTER TABLE shared_workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE shared_workflows FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shared_workflows TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON shared_workflows TO taskiem_app;

CREATE TABLE package_installs (
  tenant_id        uuid NOT NULL REFERENCES tenants(id),      -- the sub-tenant
  package_id       uuid NOT NULL,                              -- the partner's workflow: an id only
  workflow_id      uuid NOT NULL,                              -- the sub-tenant's copy
  package_version  int NOT NULL CHECK (package_version > 0),  -- the partner's version last installed
  digest           bytea NOT NULL,
  installed_by     text NOT NULL,
  installed_at     timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, package_id),
  UNIQUE (tenant_id, workflow_id),
  FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflows (tenant_id, id)
);
ALTER TABLE package_installs ENABLE ROW LEVEL SECURITY;
ALTER TABLE package_installs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON package_installs TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE (package_version, digest, installed_by, updated_at) ON package_installs TO taskiem_app;

-- +goose Down
DROP TABLE package_installs;
DROP TABLE shared_workflows;
