-- An AI build that changes a workflow reads its latest version when it
-- starts, and may be saved minutes or days later. The build records that
-- version's digest, so saving merges the proposal onto whatever was saved
-- since (three ways, as a canvas save naming its parent_digest is merged)
-- or reports the conflicts, instead of overwriting the edits in between.

-- +goose Up
ALTER TABLE ai_builds ADD COLUMN base_digest bytea;

-- +goose Down
ALTER TABLE ai_builds DROP COLUMN base_digest;
