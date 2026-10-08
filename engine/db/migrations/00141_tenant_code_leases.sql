-- +goose Up
-- The tenant-code gate's per-tenant share across API replicas (self-review
-- S34 residual). A replica about to compile or check a tenant's code takes
-- one of the tenant's numbered slots for a short lease, renewed while the
-- work runs and deleted when it ends. A replica that dies leaves its lease
-- to expire. The process-wide bound stays in each replica's memory.
CREATE TABLE tenant_code_leases (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  slot        int  NOT NULL CHECK (slot >= 0 AND slot < 1024),
  lease_id    uuid NOT NULL,
  expires_at  timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, slot)
);

ALTER TABLE tenant_code_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_code_leases FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_code_leases TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_code_leases TO taskiem_app;

-- +goose Down
DROP TABLE tenant_code_leases;
