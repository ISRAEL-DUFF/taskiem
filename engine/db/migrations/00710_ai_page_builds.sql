-- Describe it for pages (docs/spec/pages.md §10, 4; build-plan-gaps G5).
--
-- A page drafted from one sentence is an AI build as a workflow's is: the
-- same row in ai_builds (who asked, the goal redacted, the proposal, its
-- tokens and status), the same model calls in ai_interactions, the same
-- monthly budget. kind says which it is, so the workflow builder's reads
-- (GET /v1/ai/builds/{id}, its save, the chat's build) never take a page's,
-- and the page's never a workflow's. A page build once made names the
-- page it made (page_name) and is 'saved'.
-- +goose Up
ALTER TABLE ai_builds ADD COLUMN kind text NOT NULL DEFAULT 'workflow' CHECK (kind IN ('workflow', 'page'));
ALTER TABLE ai_builds ADD COLUMN page_name text CHECK (page_name ~ '^[a-z][a-z0-9-]{1,23}$');

-- +goose Down
ALTER TABLE ai_builds DROP COLUMN page_name;
ALTER TABLE ai_builds DROP COLUMN kind;
