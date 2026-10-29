-- +goose NO TRANSACTION
-- GET /v1/runs lists a tenant's runs newest first (api/runs.go): without
-- an index on (tenant_id, started_at) it read and sorted every run the
-- tenant has before returning the first page (docs/performance.md). The
-- index lets it read the page in order and stop at the limit. runs is
-- large, so the index is built CONCURRENTLY (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY runs_tenant_started ON runs (tenant_id, started_at DESC);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS runs_tenant_started;
