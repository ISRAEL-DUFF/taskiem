-- Proof of delivery (docs/dispatch.md). A delivery held one photo
-- (deliveries.proof), and a later photo replaced the earlier. Every photo
-- a rider sends is now kept here, beside the others of its delivery;
-- deliveries.proof stays the latest, as the pages show it. A photo kept
-- for the wrong order is moved to the right one (its delivery changes),
-- never copied or lost.
-- +goose Up
CREATE TABLE delivery_proofs (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  delivery_id uuid NOT NULL REFERENCES deliveries(id),
  -- The photo's place in the business's own storage.
  path        text NOT NULL CHECK (length(path) BETWEEN 1 AND 300),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX delivery_proofs_delivery ON delivery_proofs (delivery_id, created_at);
CREATE INDEX delivery_proofs_tenant ON delivery_proofs (tenant_id, created_at);

ALTER TABLE delivery_proofs ENABLE ROW LEVEL SECURITY;
ALTER TABLE delivery_proofs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON delivery_proofs TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON delivery_proofs TO taskiem_app;
GRANT UPDATE (delivery_id) ON delivery_proofs TO taskiem_app;

INSERT INTO delivery_proofs (id, tenant_id, delivery_id, path, created_at)
  SELECT gen_random_uuid(), tenant_id, id, proof, COALESCE(delivered_at, arrived_at, created_at) FROM deliveries WHERE proof IS NOT NULL;

-- +goose Down
DROP TABLE delivery_proofs;
