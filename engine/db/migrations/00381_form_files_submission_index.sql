-- +goose NO TRANSACTION
-- Deleting a form submission (retention and erasure, 00380) cascades to
-- its form_files: without an index on submission_id each delete read every
-- file row. form_files grows with every upload, so the index is built
-- CONCURRENTLY (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY form_files_submission ON form_files (submission_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS form_files_submission;
