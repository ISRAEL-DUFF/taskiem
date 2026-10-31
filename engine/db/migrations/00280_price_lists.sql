-- Business links, L4: price lists and item links (decision 0061;
-- docs/spec/business-links.md §9). A seller's price list is built on its
-- own stock items (fronts, which the spec names, are not on this branch:
-- an owner's decision): per item the price a linked buyer pays, a
-- minimum quantity and whether it is available. One list is shared with
-- many links, a link has at most one, and a link may override a price.
-- The buyer sees it as the supplier's prices: a mirror in its own
-- business, rewritten through the door whenever the list, its sharing
-- or an override changes (the link worker's 'prices' kind). An open
-- order keeps the prices it was placed at.
--
-- An item link is the buyer's: which of its own stock items a linked
-- seller's item is, so a purchase order's line reaches the seller as
-- that item, with no name matching.
-- +goose Up
CREATE TABLE price_lists (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
CREATE TABLE price_list_items (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  list_id       uuid NOT NULL REFERENCES price_lists(id) ON DELETE CASCADE,
  item_id       text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100),
  -- The item's name and unit when it was put on the list.
  name          text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
  unit          text NOT NULL DEFAULT '' CHECK (length(unit) <= 40),
  price_kobo    bigint NOT NULL CHECK (price_kobo > 0 AND price_kobo <= 100000000000),
  min_quantity  int NOT NULL DEFAULT 1 CHECK (min_quantity BETWEEN 1 AND 1000000),
  available     boolean NOT NULL DEFAULT true,
  position      int NOT NULL DEFAULT 0,
  PRIMARY KEY (list_id, item_id)
);
CREATE TABLE price_list_shares (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  link_id     uuid NOT NULL REFERENCES business_links(id),
  list_id     uuid NOT NULL REFERENCES price_lists(id) ON DELETE CASCADE,
  shared_by   text NOT NULL,
  shared_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id)
);
CREATE TABLE price_list_overrides (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  link_id     uuid NOT NULL REFERENCES business_links(id),
  item_id     text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100),
  price_kobo  bigint NOT NULL CHECK (price_kobo > 0 AND price_kobo <= 100000000000),
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id, item_id)
);

-- The buyer's mirror: the list a linked supplier shares with it.
CREATE TABLE supplier_price_lists (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  link_id     uuid NOT NULL REFERENCES business_links(id),
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id)
);
CREATE TABLE supplier_prices (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  link_id       uuid NOT NULL REFERENCES business_links(id),
  item_id       text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100), -- the seller's
  name          text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
  unit          text NOT NULL DEFAULT '' CHECK (length(unit) <= 40),
  price_kobo    bigint NOT NULL CHECK (price_kobo > 0),
  min_quantity  int NOT NULL DEFAULT 1 CHECK (min_quantity >= 1),
  available     boolean NOT NULL DEFAULT true,
  position      int NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, link_id, item_id)
);
CREATE TABLE item_links (
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  link_id         uuid NOT NULL REFERENCES business_links(id),
  item_id         text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100), -- the buyer's own
  seller_item_id  text NOT NULL CHECK (length(seller_item_id) BETWEEN 1 AND 100),
  seller_item     text NOT NULL CHECK (length(seller_item) BETWEEN 1 AND 120),
  updated_by      text NOT NULL,
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id, item_id),
  UNIQUE (tenant_id, link_id, seller_item_id)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['price_lists', 'price_list_items', 'price_list_shares', 'price_list_overrides',
                           'supplier_price_lists', 'supplier_prices', 'item_links'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- A link's prices are due to be mirrored into the buyer's business.
ALTER TABLE link_orders_due DROP CONSTRAINT link_orders_due_kind_check;
ALTER TABLE link_orders_due ADD CONSTRAINT link_orders_due_kind_check CHECK (kind IN ('order', 'mirror', 'invoice', 'receipt', 'prices'));

-- +goose Down
DELETE FROM link_orders_due WHERE kind = 'prices';
ALTER TABLE link_orders_due DROP CONSTRAINT link_orders_due_kind_check;
ALTER TABLE link_orders_due ADD CONSTRAINT link_orders_due_kind_check CHECK (kind IN ('order', 'mirror', 'invoice', 'receipt'));
DROP TABLE item_links;
DROP TABLE supplier_prices;
DROP TABLE supplier_price_lists;
DROP TABLE price_list_overrides;
DROP TABLE price_list_shares;
DROP TABLE price_list_items;
DROP TABLE price_lists;
