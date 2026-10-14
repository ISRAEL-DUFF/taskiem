-- Runs started per tenant and day, counted in 16 shards (docs/performance.md).
-- Every accepted run adds one to its tenant's row for the day, and the row
-- stays locked until the run's transaction commits (including the WAL
-- flush). With one row per tenant and day, a tenant's webhooks were
-- accepted one at a time: under load this was the largest lock wait. Each
-- start now adds to a random shard; every reader already sums the rows
-- (quotas, the limits page, partner caps) or now does (billing snapshots),
-- so the counts are unchanged.

-- +goose Up
ALTER TABLE tenant_usage ADD COLUMN shard smallint NOT NULL DEFAULT 0 CHECK (shard BETWEEN 0 AND 15);
ALTER TABLE tenant_usage DROP CONSTRAINT tenant_usage_pkey;
ALTER TABLE tenant_usage ADD PRIMARY KEY (tenant_id, day, shard);

-- +goose Down
CREATE TEMPORARY TABLE tenant_usage_folded ON COMMIT DROP AS
  SELECT tenant_id, day, sum(runs_started)::bigint AS runs_started FROM tenant_usage GROUP BY tenant_id, day;
DELETE FROM tenant_usage;
ALTER TABLE tenant_usage DROP CONSTRAINT tenant_usage_pkey;
ALTER TABLE tenant_usage DROP COLUMN shard;
ALTER TABLE tenant_usage ADD PRIMARY KEY (tenant_id, day);
INSERT INTO tenant_usage (tenant_id, day, runs_started) SELECT tenant_id, day, runs_started FROM tenant_usage_folded;
