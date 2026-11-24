-- Chat tools phase 7 (docs/spec/chat-tools.md §9): wholesale prices and
-- price deliberation between linked businesses.
--
-- A deal is an order whose prices are still being agreed: the buyer's
-- offer (an order at its own prices), the seller's counter (its change),
-- the buyer's counter back, until one side accepts. Each side keeps the
-- history on its own row (link_orders for the seller, purchase_orders for
-- the buyer), newest last; an offer lapses at deal_until.
--
-- Quantity breaks ride on a price list's items and their buyer's mirror;
-- a floor (the lowest price the seller takes without its owner) is the
-- seller's alone, never mirrored; a price kept for one buyer may end.
-- +goose Up
ALTER TABLE link_orders
  ADD COLUMN deal_history jsonb,
  ADD COLUMN deal_until timestamptz,
  -- Staff accepted below the floor: it waits for the owner's OK.
  ADD COLUMN deal_owner_at timestamptz,
  ADD COLUMN deal_owner_by text;
ALTER TABLE purchase_orders
  -- Sent at the buyer's own prices (an offer), not the supplier's.
  ADD COLUMN offer boolean NOT NULL DEFAULT false,
  ADD COLUMN deal_history jsonb,
  ADD COLUMN deal_until timestamptz;

-- [{"min_quantity": 10, "price_kobo": 3950000}, …], lowest first.
ALTER TABLE price_list_items ADD COLUMN breaks jsonb;
ALTER TABLE supplier_prices ADD COLUMN breaks jsonb;

-- A price kept for one buyer (Keep this price for them) may end.
ALTER TABLE price_list_overrides ADD COLUMN until timestamptz;

-- The lowest price per item the seller takes without its owner, and the
-- largest discount on a whole order (percent), both optional.
CREATE TABLE trade_floors (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  item_id     text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100),
  floor_kobo  bigint NOT NULL CHECK (floor_kobo > 0 AND floor_kobo <= 100000000000),
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, item_id)
);
CREATE TABLE trade_deal_settings (
  tenant_id         uuid PRIMARY KEY REFERENCES tenants(id),
  max_discount_pct  int CHECK (max_discount_pct BETWEEN 1 AND 90),
  updated_by        text NOT NULL,
  updated_at        timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE trade_floors ENABLE ROW LEVEL SECURITY;
ALTER TABLE trade_floors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON trade_floors TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON trade_floors TO taskiem_app;
ALTER TABLE trade_deal_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE trade_deal_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON trade_deal_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON trade_deal_settings TO taskiem_app;

-- +goose Down
DROP TABLE trade_deal_settings;
DROP TABLE trade_floors;
ALTER TABLE price_list_overrides DROP COLUMN until;
ALTER TABLE supplier_prices DROP COLUMN breaks;
ALTER TABLE price_list_items DROP COLUMN breaks;
ALTER TABLE purchase_orders DROP COLUMN deal_until, DROP COLUMN deal_history, DROP COLUMN offer;
ALTER TABLE link_orders DROP COLUMN deal_owner_by, DROP COLUMN deal_owner_at, DROP COLUMN deal_until, DROP COLUMN deal_history;
