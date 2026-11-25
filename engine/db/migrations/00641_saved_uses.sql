-- Each run's use of a saved thing recorded (build plan H6, g-saved-wf;
-- saved objects §6.4): when a run starts, the business's saved things its
-- workflow reads are read under the business's scope (never a person's
-- own), and one row is kept for each: which thing, which run of which
-- workflow and version, the name the workflow wrote, the steps that read
-- it, and when. The saved thing's page lists the
-- runs that read it, and the run's page the things it read. A row goes
-- with its saved thing, and with the business; it says nothing of what
-- the thing held.
-- +goose Up
CREATE TABLE saved_uses (
  tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  object_id    uuid NOT NULL REFERENCES saved_objects(id) ON DELETE CASCADE,
  run_id       uuid NOT NULL,
  workflow_id  uuid NOT NULL,
  version      int NOT NULL CHECK (version > 0),
  name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
  steps        text[] NOT NULL DEFAULT '{}' CHECK (cardinality(steps) <= 200),
  read_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (object_id, run_id)
);
CREATE INDEX saved_uses_object ON saved_uses (tenant_id, object_id, read_at DESC);
CREATE INDEX saved_uses_run ON saved_uses (tenant_id, run_id);
ALTER TABLE saved_uses ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_uses FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saved_uses TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON saved_uses TO taskiem_app;

-- +goose Down
DROP TABLE saved_uses;
