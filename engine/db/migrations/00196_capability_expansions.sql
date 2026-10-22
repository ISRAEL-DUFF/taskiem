-- +goose Up
-- Capability steps (decision 0044, R2): a workflow version that pays "by
-- whichever rail the connection is" runs as that rail's own steps. The
-- expanded definition depends on the environment's connections, so it is
-- kept apart from the version, by digest, and a run pins the one it
-- started with: changing a connection to another rail never changes a run
-- in flight. Like versions, an expansion never changes once written.
CREATE TABLE workflow_expansions (
  tenant_id   uuid  NOT NULL REFERENCES tenants(id),
  workflow_id uuid  NOT NULL,
  version     int   NOT NULL,
  digest      bytea NOT NULL,             -- sha256 of definition
  definition  jsonb NOT NULL,             -- the version with its capability steps expanded
  choices     jsonb NOT NULL DEFAULT '[]', -- each capability step's rail: step, uses, connection, connector
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workflow_id, version, digest),
  FOREIGN KEY (workflow_id, version) REFERENCES workflow_versions (workflow_id, version)
);
ALTER TABLE workflow_expansions ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_expansions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON workflow_expansions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON workflow_expansions TO taskiem_app;

-- The expansion a run runs; NULL for a version without capability steps.
ALTER TABLE runs ADD COLUMN expansion bytea;

-- The rails a deployment's capability steps run on, as the deploy found
-- them (step, uses, connection, connector); NULL without capability steps.
ALTER TABLE deployments ADD COLUMN rails jsonb;

-- +goose Down
ALTER TABLE deployments DROP COLUMN rails;
ALTER TABLE runs DROP COLUMN expansion;
DROP TABLE workflow_expansions;
