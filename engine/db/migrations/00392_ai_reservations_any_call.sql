-- Every model call reserves its tokens (finding F49), not only ai steps'
-- (00246, decision 0050): a build's, a repair's and a chat's calls also
-- counted against the monthly budget only once recorded, after the model
-- answered, so builds started at once all saw the same budget left, and
-- none of them saw the steps' reservations. Their reservations name no
-- run or step; purpose says whose call it is.

-- +goose Up
ALTER TABLE ai_reservations
  ALTER COLUMN run_id DROP NOT NULL,
  ALTER COLUMN step_id DROP NOT NULL,
  ADD COLUMN purpose text NOT NULL DEFAULT 'step';

-- +goose Down
DELETE FROM ai_reservations WHERE run_id IS NULL OR step_id IS NULL;
ALTER TABLE ai_reservations
  DROP COLUMN purpose,
  ALTER COLUMN run_id SET NOT NULL,
  ALTER COLUMN step_id SET NOT NULL;
