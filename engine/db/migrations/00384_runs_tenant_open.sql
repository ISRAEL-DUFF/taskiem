-- +goose NO TRANSACTION
-- POST /v1/pii/erase refuses while runs in flight hold the subject (spec
-- 9.4; api/admin.go runsHolding): it reads the business's runs that have
-- not ended. Without an index it went through every run the business ever
-- had. runs is large, so the index is built CONCURRENTLY (CONTRIBUTING.md,
-- Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY runs_tenant_open ON runs (tenant_id, started_at) WHERE status NOT IN ('completed', 'failed', 'cancelled');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS runs_tenant_open;
