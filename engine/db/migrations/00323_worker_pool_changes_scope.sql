-- The worker-pool change log is scoped like tenant data (F82).
-- worker_pool_changes records which tenant was moved to which pool and by
-- which operator, and its only policy for the application role was
-- USING (true): any tenant's transaction could list every tenant's
-- routing (who exists, who pays for dedicated capacity, the operators'
-- names), against threat-model B7 (accepted until now as risk R10). A
-- transaction now sees a tenant's rows only with that tenant in scope,
-- plan rows (no tenant) and all rows only in a transaction scoped to the
-- platform chain (ffffffff-...: the operators' paths, ops.Tx and the
-- console's opsTx), which is where `taskiem pools` now reads the routings.
-- +goose Up
DROP POLICY operator ON worker_pool_changes;
CREATE POLICY tenant_isolation ON worker_pool_changes FOR SELECT TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())
         OR 'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid = ANY (taskiem_tenant_scope()));
