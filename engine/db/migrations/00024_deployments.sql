-- Staging environments (spec 3.4, 15.1): each environment runs its own
-- version of a workflow. Publishing deploys to ungated environments; a
-- gated environment takes a version only by promotion from the one it
-- names, which copies the version and never edits it.

-- +goose Up
ALTER TABLE environments ADD COLUMN promotion_from text;
ALTER TABLE environments ADD CONSTRAINT environments_promotion_from
  FOREIGN KEY (tenant_id, promotion_from) REFERENCES environments(tenant_id, name);
ALTER TABLE environments ADD CONSTRAINT environments_not_self CHECK (promotion_from IS DISTINCT FROM name);

CREATE TABLE deployments (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  workflow_id   uuid NOT NULL REFERENCES workflows(id),
  environment   text NOT NULL,
  version       int NOT NULL,
  deployed_by   text,                 -- a user, key, or NULL for Git syncs
  promoted_from text,                 -- set when the version came by promotion
  deployed_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workflow_id, environment),
  FOREIGN KEY (tenant_id, environment) REFERENCES environments(tenant_id, name),
  FOREIGN KEY (workflow_id, version) REFERENCES workflow_versions(workflow_id, version)
);

INSERT INTO deployments (tenant_id, workflow_id, environment, version, deployed_by, deployed_at)
SELECT w.tenant_id, w.id, e.name, w.active_version, v.published_by::text, COALESCE(v.published_at, now())
  FROM workflows w JOIN environments e ON e.tenant_id = w.tenant_id
  JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.active_version
 WHERE w.active_version IS NOT NULL;

ALTER TABLE deployments ENABLE ROW LEVEL SECURITY;
ALTER TABLE deployments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON deployments TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON deployments TO taskiem_app;

-- +goose Down
DROP TABLE deployments;
ALTER TABLE environments DROP CONSTRAINT environments_not_self;
ALTER TABLE environments DROP CONSTRAINT environments_promotion_from;
ALTER TABLE environments DROP COLUMN promotion_from;
