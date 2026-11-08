-- A separate allowance for AI steps in runs (decision 0062 D7, plan V4,
-- A7 and A8): an assistant answering customers spends from
-- ai_step_monthly_tokens, never from ai_monthly_tokens, which building,
-- repair and chat routing keep. 0 turns AI steps off for the plan (where
-- ai_monthly_tokens' 0 means no check). Calls are told apart by
-- ai_interactions.kind: step and step_correct are AI steps.

-- +goose Up
ALTER TABLE tenant_limits
  ADD COLUMN ai_step_monthly_tokens bigint CHECK (ai_step_monthly_tokens >= 0);

-- +goose Down
ALTER TABLE tenant_limits DROP COLUMN ai_step_monthly_tokens;
