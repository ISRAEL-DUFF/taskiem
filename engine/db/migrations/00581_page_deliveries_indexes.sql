-- +goose NO TRANSACTION
-- Pages (docs/spec/pages.md §5, §9): the deliveries record view lists a
-- business's deliveries newest first, by status too, and an open page asks
-- which changed in the last few seconds (changed_at, 00580). deliveries is
-- large, so the indexes are built CONCURRENTLY (CONTRIBUTING.md,
-- Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS deliveries_status ON deliveries (tenant_id, status, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS deliveries_changed_at ON deliveries (tenant_id, changed_at);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS deliveries_changed_at;
DROP INDEX CONCURRENTLY IF EXISTS deliveries_status;
