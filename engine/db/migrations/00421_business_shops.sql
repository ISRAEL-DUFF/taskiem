-- Business chat tools (docs/spec/business-chat-tools.md §2): a linked
-- business sees the other's storefront where no price list is shared.
-- +goose Up

-- The buyer's copy of a seller's storefront item keeps its photo (the
-- storefront's urls by width) and how many are left, where it counts them.
ALTER TABLE supplier_prices ADD COLUMN photo jsonb CHECK (photo IS NULL OR jsonb_typeof(photo) = 'object');
ALTER TABLE supplier_prices ADD COLUMN left_count bigint CHECK (left_count IS NULL OR left_count >= 0);

-- What each link's copy was last written from, in the seller's business:
-- a storefront change that changes nothing a link sees writes nothing.
CREATE TABLE link_shop_offers (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  link_id     uuid NOT NULL REFERENCES business_links(id),
  hash        text NOT NULL CHECK (length(hash) = 64),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id)
);
ALTER TABLE link_shop_offers ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_shop_offers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_shop_offers TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON link_shop_offers TO taskiem_app;

-- Links already active: each side's offer reaches the other once.
INSERT INTO link_orders_due (tenant_id, kind, object_id)
SELECT a_tenant, 'prices', id FROM business_links WHERE status = 'active'
UNION
SELECT b_tenant, 'prices', id FROM business_links WHERE status = 'active'
ON CONFLICT (tenant_id, kind, object_id) DO NOTHING;

-- +goose Down
DROP TABLE link_shop_offers;
ALTER TABLE supplier_prices DROP COLUMN left_count;
ALTER TABLE supplier_prices DROP COLUMN photo;
