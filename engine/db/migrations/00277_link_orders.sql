-- Business links, L2: orders (decision 0061; docs/spec/business-links.md
-- §7). A purchase order to a supplier that is a linked business becomes an
-- order in the seller's business: a storefront order of the kind trade,
-- with the buyer as its customer, so it is on the seller's Orders page,
-- starts its order workflow, and is delivered by its riders like any
-- order. What a linked order has beyond a storefront order is in
-- link_orders beside it. The seller's steps flow back to the buyer's
-- purchase order as mirrored fields.
--
-- Who owns what:
--   * the buyer's purchase order: its lines, status and receipts are the
--     buyer's; seller_status, seller_note, change, delivered_at,
--     short_agreed_at and each line's "delivered" are mirrored from the
--     seller and never set by the buyer; cancel_asked_at and short_at are
--     the buyer's own.
--   * the seller's order: its status, basket, prices, change, seller_note
--     and invoice are the seller's; the buyer's note, cancel_asked_at,
--     short and receipt_at are mirrored from the buyer.
--
-- Sending is noted by a trigger (the send happens inside callers'
-- transactions that write more afterwards) and a worker writes the order
-- through taskiem_link_enter. The buyer's steps (cancel, accept a change,
-- confirm an order the seller drafted, say what came) write their own
-- record, then the seller's through the door, in one transaction. The
-- seller's order moves by many paths (its own steps, the Orders page, a
-- workflow, a rider), so a change to it is noted by a trigger, and the
-- worker mirrors the order as it then is to the purchase order.
-- +goose Up

-- A supplier that is a linked business: its orders go through the link.
ALTER TABLE suppliers ADD COLUMN link_id uuid REFERENCES business_links(id);
CREATE UNIQUE INDEX suppliers_link ON suppliers (tenant_id, link_id) WHERE retired_at IS NULL AND link_id IS NOT NULL;

-- Where a linked supplier delivers the business's orders, sealed:
-- {address, phone}, the address and the number a rider calls at the door.
ALTER TABLE stock_settings ADD COLUMN deliver_to jsonb;

ALTER TABLE purchase_orders
  ADD COLUMN link_id         uuid REFERENCES business_links(id),
  ADD COLUMN seller_status   text CHECK (seller_status IN ('proposed', 'received', 'changed', 'accepted', 'ready', 'dispatched', 'delivered', 'declined', 'cancelled')),
  ADD COLUMN seller_note     text CHECK (length(seller_note) <= 500),
  -- The seller's proposed lines while seller_status is changed:
  -- [{item_id, item, unit, quantity, cost_kobo}].
  ADD COLUMN change          jsonb CHECK (change IS NULL OR jsonb_typeof(change) = 'array'),
  ADD COLUMN delivered_at    timestamptz,
  ADD COLUMN short_agreed_at timestamptz,
  ADD COLUMN cancel_asked_at timestamptz,
  ADD COLUMN short_at        timestamptz;

-- The seller's side: a storefront order of the kind trade, which needs no
-- storefront (a business may sell to businesses without one); proposed
-- (drafted by the seller for the buyer, waiting for its Confirm) and
-- changed (the seller proposes other quantities or prices) are its own.
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'trade'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_status_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_status_check CHECK (status IN ('received', 'awaiting_payment', 'paid', 'accepted',
  'declined', 'expired', 'completed', 'cancelled', 'refund_due', 'no_show', 'price_changed', 'refunded', 'dispatched', 'on_the_way', 'arrived',
  'proposed', 'changed'));
ALTER TABLE storefront_orders ALTER COLUMN storefront_id DROP NOT NULL;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_trade CHECK ((kind = 'trade') OR storefront_id IS NOT NULL);

CREATE TABLE link_orders (
  order_id         uuid PRIMARY KEY REFERENCES storefront_orders(id),
  tenant_id        uuid NOT NULL REFERENCES tenants(id),          -- the seller
  link_id          uuid NOT NULL REFERENCES business_links(id),
  buyer_tenant     uuid NOT NULL REFERENCES tenants(id),
  buyer_name       text NOT NULL CHECK (length(buyer_name) BETWEEN 1 AND 200),
  source_id        uuid NOT NULL,                                  -- the buyer's purchase order
  -- The buyer's reference, the one both sides say (the order's own is the
  -- same unless another order has it).
  purchase_reference text NOT NULL CHECK (purchase_reference ~ '^P-[A-Z0-9]{6}$'),
  -- [{item, unit, quantity, price_kobo, delivered, item_id, matched}]: the
  -- buyer's items, at the price agreed (the seller's catalogue price for an
  -- item matched to its own, else the buyer's proposed price until the
  -- seller changes it), what the seller delivered of each, and the
  -- seller's own catalogue row (item_id) when matched: by a link the
  -- seller confirmed (matched "linked") or by name, to confirm ("name").
  -- The order's basket and total are kept from them.
  lines            jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array' AND jsonb_array_length(lines) BETWEEN 1 AND 50),
  -- The buyer's note on its order (the order's own note is Taskiem's).
  note             text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  placed_by_seller boolean NOT NULL DEFAULT false,
  change           jsonb CHECK (change IS NULL OR jsonb_typeof(change) = 'array'),
  seller_note      text CHECK (length(seller_note) <= 500),
  ready_at         timestamptz,
  delivered_at     timestamptz,
  -- Mirrored from the buyer: a cancel asked after acceptance; Not all came
  -- (what the buyer counted, by line); its receipt settled.
  cancel_asked_at  timestamptz,
  short            jsonb CHECK (short IS NULL OR jsonb_typeof(short) = 'array'),
  short_at         timestamptz,
  receipt_at       timestamptz,
  short_agreed_at  timestamptz,
  invoice_id       uuid,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, link_id, source_id)
);
ALTER TABLE link_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_orders TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON link_orders TO taskiem_app;

-- The seller's choices per link: the invoice for an order is raised when it
-- accepts it, or when the buyer's receipt is settled after delivery (the
-- buyer's tap, or a day with no Not all came); and whether its orders are
-- accepted by themselves when they arrive priced.
CREATE TABLE trade_settings (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  link_id     uuid NOT NULL REFERENCES business_links(id),
  invoice_at  text NOT NULL DEFAULT 'delivered' CHECK (invoice_at IN ('accepted', 'delivered')),
  auto_accept boolean NOT NULL DEFAULT false,
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id)
);
ALTER TABLE trade_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE trade_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON trade_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON trade_settings TO taskiem_app;

-- The seller's own items a linked buyer's item names are (§17: asked once
-- per item, then remembered): the buyer's name for it, lowered, and the
-- seller's catalogue row.
CREATE TABLE trade_item_links (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  link_id     uuid NOT NULL REFERENCES business_links(id),
  name_key    text NOT NULL CHECK (length(name_key) BETWEEN 1 AND 120),
  item_id     text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100),
  item        text NOT NULL CHECK (length(item) BETWEEN 1 AND 120),
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id, name_key)
);
ALTER TABLE trade_item_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE trade_item_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON trade_item_links TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON trade_item_links TO taskiem_app;

-- Work due on linked orders, each in the business it starts in: an order
-- sent and still to be written into the seller's business (kind order,
-- the buyer's purchase order); the seller's order to mirror to the buyer
-- (mirror, the seller's order); an invoice still to be raised (invoice,
-- the seller's order); a delivery whose receipt settles by itself a day
-- after it (receipt, the buyer's purchase order).
CREATE TABLE link_orders_due (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  kind        text NOT NULL CHECK (kind IN ('order', 'mirror', 'invoice', 'receipt')),
  object_id   uuid NOT NULL,
  due_at      timestamptz NOT NULL DEFAULT now(),
  tries       int NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, kind, object_id)
);
ALTER TABLE link_orders_due ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_orders_due FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_orders_due TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON link_orders_due TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON link_orders_due TO taskiem_app;
GRANT SELECT, INSERT, UPDATE ON link_orders_due TO taskiem_dispatch;

-- A purchase order sent to a linked supplier is due to be written into the
-- seller's business.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_order_note() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO link_orders_due (tenant_id, kind, object_id) VALUES (NEW.tenant_id, 'order', NEW.id)
  ON CONFLICT (tenant_id, kind, object_id) DO UPDATE SET due_at = now(), tries = 0;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER purchase_order_link_send AFTER UPDATE ON purchase_orders
  FOR EACH ROW WHEN (NEW.link_id IS NOT NULL AND NEW.status = 'sent' AND OLD.status = 'drafted')
  EXECUTE FUNCTION taskiem_link_order_note();

-- The seller's order changed: due to be mirrored to the buyer. As its
-- owner, so a change by any role (a rider's tap runs as the application
-- too, but checks and sweeps may not) is noted.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_order_mirror_note() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  -- Both tables call it: the order's id is link_orders.order_id, or the
  -- storefront order's own id.
  INSERT INTO link_orders_due (tenant_id, kind, object_id)
    VALUES (NEW.tenant_id, 'mirror', (to_jsonb(NEW) ->> CASE WHEN TG_TABLE_NAME = 'link_orders' THEN 'order_id' ELSE 'id' END)::uuid)
  ON CONFLICT (tenant_id, kind, object_id) DO UPDATE SET due_at = now(), tries = 0;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_order_mirror_note() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_order_mirror_note() FROM PUBLIC;
CREATE TRIGGER storefront_order_link_mirror AFTER UPDATE ON storefront_orders
  FOR EACH ROW WHEN (NEW.kind = 'trade' AND OLD.status IS DISTINCT FROM NEW.status)
  EXECUTE FUNCTION taskiem_link_order_mirror_note();
CREATE TRIGGER link_order_mirror AFTER UPDATE ON link_orders
  FOR EACH ROW EXECUTE FUNCTION taskiem_link_order_mirror_note();

-- The worker's claim: linked-order work due, across businesses, oldest
-- first, each held with back-off; ids only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_orders_claim(p_limit int)
RETURNS TABLE (tenant_id uuid, kind text, object_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE link_orders_due d SET due_at = now() + interval '1 minute' * LEAST(60, power(2, d.tries)), tries = d.tries + 1
   WHERE (d.tenant_id, d.kind, d.object_id) IN (SELECT x.tenant_id, x.kind, x.object_id FROM link_orders_due x WHERE x.due_at <= now()
                                                ORDER BY x.due_at LIMIT LEAST(GREATEST(p_limit, 1), 100) FOR UPDATE SKIP LOCKED)
  RETURNING d.tenant_id, d.kind, d.object_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_orders_claim(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_orders_claim(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_orders_claim(int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_link_orders_claim(int);
DROP TRIGGER link_order_mirror ON link_orders;
DROP TRIGGER storefront_order_link_mirror ON storefront_orders;
DROP FUNCTION taskiem_link_order_mirror_note();
DROP TRIGGER purchase_order_link_send ON purchase_orders;
DROP FUNCTION taskiem_link_order_note();
DROP TABLE link_orders_due;
DROP TABLE trade_item_links;
DROP TABLE trade_settings;
DROP TABLE link_orders;
DELETE FROM storefront_orders WHERE kind = 'trade' OR status IN ('proposed', 'changed');
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_trade;
ALTER TABLE storefront_orders ALTER COLUMN storefront_id SET NOT NULL;
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_status_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_status_check CHECK (status IN ('received', 'awaiting_payment', 'paid', 'accepted',
  'declined', 'expired', 'completed', 'cancelled', 'refund_due', 'no_show', 'price_changed', 'refunded', 'dispatched', 'on_the_way', 'arrived'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking'));
ALTER TABLE purchase_orders DROP COLUMN short_at, DROP COLUMN cancel_asked_at, DROP COLUMN short_agreed_at, DROP COLUMN delivered_at, DROP COLUMN change,
  DROP COLUMN seller_status, DROP COLUMN seller_note, DROP COLUMN link_id;
ALTER TABLE stock_settings DROP COLUMN deliver_to;
DROP INDEX suppliers_link;
ALTER TABLE suppliers DROP COLUMN link_id;
