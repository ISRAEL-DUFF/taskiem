-- Workflow definitions. Spec 3.4, 5.2.

-- +goose Up
CREATE TABLE workflows (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  name            text NOT NULL,
  git_path        text,                            -- e.g. flows/disburse_loan.wd.json
  active_version  int,
  created_by      uuid NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id)
);

CREATE TABLE workflow_versions (
  workflow_id   uuid NOT NULL,
  version       int  NOT NULL CHECK (version >= 1),
  tenant_id     uuid NOT NULL,
  definition    jsonb NOT NULL,                    -- canonical WD, immutable
  layout        jsonb,                             -- canvas positions, mutable
  digest        bytea NOT NULL CHECK (length(digest) = 32),
  state         text NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'published', 'deprecated', 'archived')),
  git_commit    text,
  published_by  uuid,
  published_at  timestamptz,
  PRIMARY KEY (workflow_id, version),
  FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflows (tenant_id, id)
);

-- Definitions are immutable once written; only layout and lifecycle fields change.
-- +goose StatementBegin
CREATE FUNCTION taskiem_workflow_versions_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.definition IS DISTINCT FROM OLD.definition
     OR NEW.digest IS DISTINCT FROM OLD.digest
     OR NEW.workflow_id IS DISTINCT FROM OLD.workflow_id
     OR NEW.version IS DISTINCT FROM OLD.version
     OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id THEN
    RAISE EXCEPTION 'workflow version % of % is immutable; save a new version', OLD.version, OLD.workflow_id
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER workflow_versions_immutable BEFORE UPDATE ON workflow_versions
  FOR EACH ROW EXECUTE FUNCTION taskiem_workflow_versions_immutable();

ALTER TABLE workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflows FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON workflows TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

ALTER TABLE workflow_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON workflow_versions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

GRANT SELECT, INSERT, UPDATE ON workflows, workflow_versions TO taskiem_app;
