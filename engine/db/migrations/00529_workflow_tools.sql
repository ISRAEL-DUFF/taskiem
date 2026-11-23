-- Made tools M1 (decision 0073, made tools §18): the index of deployed
-- tools, a row for each workflow deployed in an environment with a tool
-- block, written by the transaction that deploys it and gone with the
-- deployment (the foreign key), so it never disagrees with what is
-- deployed. One command per business and environment. Its owner's
-- "always ask" (never less careful than its steps) is kept across deploys.
-- And tool_confirms: a start waiting for its yes (or a passkey), holding
-- only a digest of the values, never the values.
-- +goose Up
CREATE TABLE workflow_tools (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  workflow_id  uuid NOT NULL,
  environment  text NOT NULL,
  version      int NOT NULL,
  command      text NOT NULL CHECK (command ~ '^[a-z][a-z0-9-]{1,23}$'),
  name         text NOT NULL,
  about        text NOT NULL,
  says         text[] NOT NULL DEFAULT '{}',
  icon         text NOT NULL DEFAULT '',
  audience     text NOT NULL CHECK (audience IN ('members', 'customers', 'linked')),
  roles        text[] NOT NULL DEFAULT '{}',
  care         jsonb NOT NULL CHECK (care->>'level' IN ('answers', 'yes', 'step_up')),
  answer_type  text NOT NULL DEFAULT '' CHECK (answer_type IN ('', 'text', 'card', 'list')),
  always_ask   boolean NOT NULL DEFAULT false,
  deployed_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workflow_id, environment),
  UNIQUE (tenant_id, environment, command),
  FOREIGN KEY (workflow_id, environment) REFERENCES deployments(workflow_id, environment) ON DELETE CASCADE
);
ALTER TABLE workflow_tools ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_tools FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON workflow_tools TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON workflow_tools TO taskiem_app;

CREATE TABLE tool_confirms (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  user_id      uuid NOT NULL REFERENCES users(id),
  workflow_id  uuid NOT NULL REFERENCES workflows(id),
  version      int NOT NULL,
  environment  text NOT NULL,
  command      text NOT NULL,
  care         text NOT NULL CHECK (care IN ('yes', 'step_up')),
  input_digest bytea NOT NULL,
  channel      text NOT NULL,
  expires_at   timestamptz NOT NULL,
  run_id       uuid,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tool_confirms_expiry ON tool_confirms (expires_at);
ALTER TABLE tool_confirms ENABLE ROW LEVEL SECURITY;
ALTER TABLE tool_confirms FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tool_confirms TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON tool_confirms TO taskiem_app;

-- +goose Down
DROP TABLE tool_confirms;
DROP TABLE workflow_tools;
