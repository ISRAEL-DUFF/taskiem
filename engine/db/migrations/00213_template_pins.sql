-- +goose Up
-- Template steps (decision 0047): a workflow version that uses a callable
-- template runs that template as it was when the version was saved. Each
-- template a version uses, at any depth, is kept with it, so a library
-- update never changes a version already saved, and the workflow's page
-- can offer the update with its differences. Like versions, a pin never
-- changes once written.
CREATE TABLE workflow_template_pins (
  tenant_id   uuid  NOT NULL REFERENCES tenants(id),
  workflow_id uuid  NOT NULL,
  version     int   NOT NULL,
  template_id text  NOT NULL CHECK (template_id ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
  digest      bytea NOT NULL,             -- sha256 of template
  template    jsonb NOT NULL,             -- the template as the library had it (templates.Parse reads it)
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workflow_id, version, template_id),
  FOREIGN KEY (workflow_id, version) REFERENCES workflow_versions (workflow_id, version)
);
ALTER TABLE workflow_template_pins ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_template_pins FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON workflow_template_pins TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON workflow_template_pins TO taskiem_app;

-- +goose Down
DROP TABLE workflow_template_pins;
