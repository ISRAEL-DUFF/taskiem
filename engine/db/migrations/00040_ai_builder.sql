-- The AI workflow builder (spec 12.1, 12.3). Every model call is one row
-- in ai_interactions: the prompt as it left the process (redacted; the
-- large, stable system prompt by digest), the redacted answer, the model,
-- token usage and what the builder did with it. Rows are insert-only. A
-- build (ai_builds) is one request and its proposal; saving a proposal
-- creates a draft workflow version that records the build it came from
-- (workflow_versions.ai_build_id), so the AI shows as co-author.

-- +goose Up
CREATE TABLE ai_builds (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  actor             text NOT NULL,                 -- the person who asked
  goal              text NOT NULL,                 -- redacted
  workflow_id       uuid,                          -- the workflow being modified, if any
  environment       text NOT NULL DEFAULT '',
  status            text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'proposed', 'failed', 'saved')),
  stage             text NOT NULL DEFAULT 'queued',
  error             text,
  provider          text NOT NULL,
  model             text NOT NULL,
  proposal          jsonb,                         -- definition, summary, warnings, tests, results
  rounds            int NOT NULL DEFAULT 0,
  valid             boolean,
  total_tokens      bigint NOT NULL DEFAULT 0,
  saved_workflow_id uuid,
  saved_version     int,
  created_at        timestamptz NOT NULL DEFAULT now(),
  finished_at       timestamptz,
  saved_at          timestamptz
);
CREATE INDEX ai_builds_tenant ON ai_builds (tenant_id, created_at DESC);
ALTER TABLE ai_builds ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_builds FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_builds TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON ai_builds TO taskiem_app;

CREATE TABLE ai_interactions (
  id                    uuid PRIMARY KEY,
  tenant_id             uuid NOT NULL REFERENCES tenants(id),
  build_id              uuid REFERENCES ai_builds(id),
  round                 int NOT NULL,
  kind                  text NOT NULL,             -- draft | correct
  actor                 text NOT NULL,
  provider              text NOT NULL,
  model                 text NOT NULL,             -- the model that answered
  system_digest         text NOT NULL,             -- sha256 of the stable system prompt
  messages              jsonb NOT NULL,            -- redacted, as sent
  response              text,                      -- redacted
  stop_reason           text,
  outcome               text NOT NULL,             -- valid | problems | unparseable | refused | truncated | error
  error                 text,
  input_tokens          bigint NOT NULL DEFAULT 0,
  output_tokens         bigint NOT NULL DEFAULT 0,
  cache_creation_tokens bigint NOT NULL DEFAULT 0,
  cache_read_tokens     bigint NOT NULL DEFAULT 0,
  total_tokens          bigint NOT NULL DEFAULT 0,
  created_at            timestamptz NOT NULL DEFAULT now()
);
-- Budgets sum a tenant's tokens this month.
CREATE INDEX ai_interactions_tenant ON ai_interactions (tenant_id, created_at);
CREATE INDEX ai_interactions_build ON ai_interactions (build_id);
ALTER TABLE ai_interactions ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_interactions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON ai_interactions FOR SELECT TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY tenant_record ON ai_interactions FOR INSERT TO taskiem_app WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
-- Insert-only: an interaction is never changed or removed.
GRANT SELECT, INSERT ON ai_interactions TO taskiem_app;

-- A version saved from an AI proposal names it: created_by stays the
-- person who saved it; the build is the AI co-author.
ALTER TABLE workflow_versions ADD COLUMN ai_build_id uuid REFERENCES ai_builds(id);

-- +goose Down
ALTER TABLE workflow_versions DROP COLUMN ai_build_id;
DROP TABLE ai_interactions;
DROP TABLE ai_builds;
