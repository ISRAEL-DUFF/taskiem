-- +goose NO TRANSACTION
-- taskiem_form_submissions_expire (00394) looks for submissions that
-- started no run and are past their retain_until; without an index it read
-- every submission. form_submissions grows with every answer, so the index
-- is built CONCURRENTLY (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY form_submissions_runless_retain ON form_submissions (retain_until) WHERE run_id IS NULL AND retain_until IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS form_submissions_runless_retain;
