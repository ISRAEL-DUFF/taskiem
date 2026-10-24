-- +goose Up
-- AI steps (decision 0050): a workflow asks the deployment's model at run
-- time. Each call is an ai_interactions row like a build's or a repair's,
-- keyed by the run and step instance, so it counts against the tenant's
-- monthly AI budget and is visible where the others are. A business may
-- turn AI steps off (governance_settings.ai_steps): publishing refuses a
-- workflow with one, and a step already published fails when it starts.
ALTER TABLE ai_interactions ADD COLUMN run_id uuid;
ALTER TABLE ai_interactions ADD COLUMN step_id text;
CREATE INDEX ai_interactions_run ON ai_interactions (run_id) WHERE run_id IS NOT NULL;
ALTER TABLE governance_settings ADD COLUMN ai_steps boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE governance_settings DROP COLUMN ai_steps;
DROP INDEX ai_interactions_run;
ALTER TABLE ai_interactions DROP COLUMN step_id;
ALTER TABLE ai_interactions DROP COLUMN run_id;
