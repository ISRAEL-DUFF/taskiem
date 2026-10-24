-- +goose Up
-- Subflow steps (decision 0049): a workflow runs another workflow of the
-- business as a child run. The child keeps which step of which run
-- started it (parent_run_id, already there for forks, and parent_step),
-- and whether that step waits for it (parent_waits): a parent that ends
-- cancels the children it was waiting for, and leaves the others to
-- finish on their own. A fork has parent_run_id and forked_from_seq and
-- no parent_step.
ALTER TABLE runs ADD COLUMN parent_step text;
ALTER TABLE runs ADD COLUMN parent_waits boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE runs DROP COLUMN parent_waits;
ALTER TABLE runs DROP COLUMN parent_step;
