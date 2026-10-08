-- Regression tests a workflow keeps on the platform (spec 10.5, 12.2): a
-- repair's test for the failing case is added when the repair is accepted,
-- and every later repair of the workflow must pass all of them. Cases are
-- wd-test/v1 cases built from redacted data, never the run's own values.

-- +goose Up
CREATE TABLE workflow_tests (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  workflow_id uuid NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
  name        text NOT NULL,
  test        jsonb NOT NULL,                       -- one wd-test/v1 case
  policies    jsonb,                                -- approval policies it runs with, by name
  source      text NOT NULL CHECK (source IN ('repair', 'person')),
  repair_id   uuid REFERENCES repair_proposals(id) ON DELETE SET NULL,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workflow_id, name)
);
ALTER TABLE workflow_tests ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_tests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON workflow_tests TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON workflow_tests TO taskiem_app;

-- +goose Down
DROP TABLE workflow_tests;
