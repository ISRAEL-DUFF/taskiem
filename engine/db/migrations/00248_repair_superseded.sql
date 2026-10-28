-- A repair proposal patches the version its run failed on. When versions
-- were published after that one, accepting it merges the patch onto the
-- latest published version (three ways, as a canvas save is merged); a
-- patch whose change conflicts with theirs, or that no longer passes its
-- tests once merged, can no longer be accepted and is marked superseded.

-- +goose Up
ALTER TABLE repair_proposals DROP CONSTRAINT repair_proposals_status_check;
ALTER TABLE repair_proposals ADD CONSTRAINT repair_proposals_status_check CHECK (status IN ('analysing', 'proposed', 'action', 'withheld', 'failed',
  'awaiting_publish', 'awaiting_promotion', 'published', 'resumed', 'dismissed', 'superseded'));

-- +goose Down
UPDATE repair_proposals SET status = 'dismissed' WHERE status = 'superseded';
ALTER TABLE repair_proposals DROP CONSTRAINT repair_proposals_status_check;
ALTER TABLE repair_proposals ADD CONSTRAINT repair_proposals_status_check CHECK (status IN ('analysing', 'proposed', 'action', 'withheld', 'failed',
  'awaiting_publish', 'awaiting_promotion', 'published', 'resumed', 'dismissed'));
