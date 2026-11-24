-- Who made a Git-led version (docs/git.md, "Who made a Git-led version").
-- A sync wrote its versions as "git:<env>", so maker-checker had nobody to
-- compare an approver with: the engineer who committed a payout workflow
-- could approve its payouts. Each verified push to a connected branch is
-- now kept with the people the Git host names behind it, and a version a
-- sync creates records them as its makers, or that they are not known.
-- +goose Up
CREATE TABLE git_pushes (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  commit       text NOT NULL,
  before       text NOT NULL DEFAULT '',       -- the branch's commit before the push; empty: not said
  people       jsonb NOT NULL DEFAULT '[]',    -- [{role, email, login}] as the host names them
  complete     boolean NOT NULL DEFAULT false, -- people covers every commit of the push
  received_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment, commit)
);
ALTER TABLE git_pushes ENABLE ROW LEVEL SECURITY;
ALTER TABLE git_pushes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON git_pushes TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON git_pushes TO taskiem_app;

-- makers: the members behind the commits and the push or merge.
-- maker_unknown: someone behind it could not be matched to a member, or
-- the commits since the last deploy are not all accounted for. Versions
-- Git deployed before this migration have nobody recorded.
ALTER TABLE workflow_versions ADD COLUMN makers uuid[] NOT NULL DEFAULT '{}',
  ADD COLUMN maker_unknown boolean NOT NULL DEFAULT false;
ALTER TABLE workflow_versions DISABLE TRIGGER workflow_versions_immutable;
UPDATE workflow_versions SET maker_unknown = true WHERE created_by LIKE 'git:%';
ALTER TABLE workflow_versions ENABLE TRIGGER workflow_versions_immutable;

-- Who made a version does not change after it is written.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_workflow_versions_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.definition IS DISTINCT FROM OLD.definition
     OR NEW.digest IS DISTINCT FROM OLD.digest
     OR NEW.workflow_id IS DISTINCT FROM OLD.workflow_id
     OR NEW.version IS DISTINCT FROM OLD.version
     OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
     OR NEW.makers IS DISTINCT FROM OLD.makers
     OR NEW.maker_unknown IS DISTINCT FROM OLD.maker_unknown THEN
    RAISE EXCEPTION 'workflow version % of % is immutable; save a new version', OLD.version, OLD.workflow_id
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_workflow_versions_immutable() RETURNS trigger
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
ALTER TABLE workflow_versions DROP COLUMN maker_unknown, DROP COLUMN makers;
DROP TABLE git_pushes;
