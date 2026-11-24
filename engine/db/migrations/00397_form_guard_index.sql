-- +goose NO TRANSACTION
-- taskiem_form_guards (00396) is read by every api process every 10
-- seconds for the forms guarded now; without an index it read every form
-- of every business. The index is built CONCURRENTLY (CONTRIBUTING.md,
-- Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY form_endpoints_guarded ON form_endpoints (guarded_until) WHERE guarded_until IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS form_endpoints_guarded;
