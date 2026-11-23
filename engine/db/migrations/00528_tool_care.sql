-- Made tools (decision 0073): how much a tool asks before it runs (care)
-- is worked out from its steps when its version is published, never
-- declared, and kept with the version: {level, because}. NULL for a
-- version that is not a tool.
-- +goose Up
ALTER TABLE workflow_versions ADD COLUMN tool_care jsonb
  CHECK (tool_care IS NULL OR tool_care->>'level' IN ('answers', 'yes', 'step_up'));

-- +goose Down
ALTER TABLE workflow_versions DROP COLUMN tool_care;
