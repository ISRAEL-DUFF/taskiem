-- Business links, L2: orders (decision 0061; docs/spec/business-links.md
-- §7). A purchase order to a supplier that is a linked business becomes an
-- order in the seller's business (trade_orders), and the seller's accept,
-- change, decline, ready, dispatch, delivery and cancel flow back to the
-- buyer's purchase order as mirrored fields.
--
-- Who owns what:
--   * the buyer's purchase order: its lines, status and receipts are the
--     buyer's; seller_status, seller_note, change, delivered_at and each
--     line's "delivered" are mirrored from the seller and never set by the
--     buyer; cancel_asked_at and short_at are the buyer's own.
--   * the seller's trade order: its status, prices, change, seller_note and
--     invoice are the seller's; the buyer's lines as ordered, its note, and
--     cancel_asked_at, short and receipt_at are mirrored from the buyer.
--
-- Sending is noted by a trigger (the send happens inside callers'
-- transactions that write more afterwards) and a worker writes the order
-- through taskiem_link_enter. Every later step is one transaction: the
-- side that acts writes its own record, then enters the other through the
-- door and writes the mirror, so the two cannot disagree.
-- +goose Up

-- A supplier that is a linked business: its orders go through the link.
ALTER TABLE suppliers ADD COLUMN link_id uuid REFERENCES business_links(id);
CREATE UNIQUE INDEX suppliers_link ON suppliers (tenant_id, link_id) WHERE retired_at IS NULL AND link_id IS NOT NULL;

ALTER TABLE purchase_orders
  ADD COLUMN link_id         uuid REFERENCES business_links(id),
  ADD COLUMN seller_status   text CHECK (seller_status IN ('received', 'changed', 'accepted', 'ready', 'dispatched', 'delivered', 'declined', 'cancelled')),
  ADD COLUMN seller_note     text CHECK (length(seller_note) <= 500),
  -- The seller's proposed lines while seller_status is changed:
  -- [{item_id, item, unit, quantity, cost_kobo}].
  ADD COLUMN change          jsonb CHECK (change IS NULL OR jsonb_typeof(change) = 'array'),
  ADD COLUMN delivered_at    timestamptz,
  ADD COLUMN cancel_asked_at timestamptz,
  ADD COLUMN short_at        timestamptz;

CREATE TABLE trade_orders (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),          -- the seller
  link_id          uuid NOT NULL REFERENCES business_links(id),
  buyer_tenant     uuid NOT NULL REFERENCES tenants(id),
  buyer_name       text NOT NULL CHECK (length(buyer_name) BETWEEN 1 AND 200),
  source_id        uuid NOT NULL,                                  -- the buyer's purchase order
  reference        text NOT NULL CHECK (reference ~ '^P-[A-Z0-9]{6}$'),
  -- [{item, unit, quantity, price_kobo, delivered}]: the buyer's items,
  -- at the price agreed (the buyer's proposed price until the seller
  -- changes it), and what the seller delivered of each.
  lines            jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array' AND jsonb_array_length(lines) BETWEEN 1 AND 50),
  total_kobo       bigint NOT NULL CHECK (total_kobo >= 0),
  note             text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  status           text NOT NULL DEFAULT 'received'
                     CHECK (status IN ('received', 'changed', 'accepted', 'ready', 'dispatched', 'delivered', 'declined', 'cancelled')),
  change           jsonb CHECK (change IS NULL OR jsonb_typeof(change) = 'array'),
  seller_note      text CHECK (length(seller_note) <= 500),
  -- Mirrored from the buyer: a cancel asked after acceptance; Not all came
  -- (what the buyer counted, by line); its receipt settled.
  cancel_asked_at  timestamptz,
  short            jsonb CHECK (short IS NULL OR jsonb_typeof(short) = 'array'),
  short_at         timestamptz,
  receipt_at       timestamptz,
  short_agreed_at  timestamptz,
  invoice_id       uuid,
  decided_by       text,
  accepted_at      timestamptz,
  delivered_at     timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, link_id, source_id)
);
CREATE INDEX trade_orders_list ON trade_orders (tenant_id, created_at DESC);
ALTER TABLE trade_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE trade_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON trade_orders TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON trade_orders TO taskiem_app;

-- The seller's choice per link: the invoice for an order is raised when it
-- accepts it, or when the buyer's receipt is settled after delivery (the
-- buyer's tap, or a day with no Not all came).
CREATE TABLE trade_settings (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  link_id     uuid NOT NULL REFERENCES business_links(id),
  invoice_at  text NOT NULL DEFAULT 'delivered' CHECK (invoice_at IN ('accepted', 'delivered')),
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id)
);
ALTER TABLE trade_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE trade_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON trade_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON trade_settings TO taskiem_app;

-- Work due on linked orders, each in the business it starts in: an order
-- sent and still to be written into the seller's business (kind order,
-- the buyer's purchase order); an invoice still to be raised (invoice, the
-- seller's trade order); a delivery whose receipt settles by itself a day
-- after it (receipt, the buyer's purchase order).
CREATE TABLE link_orders_due (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  kind        text NOT NULL CHECK (kind IN ('order', 'invoice', 'receipt')),
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
GRANT SELECT, UPDATE ON link_orders_due TO taskiem_dispatch;

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
DROP TRIGGER purchase_order_link_send ON purchase_orders;
DROP FUNCTION taskiem_link_order_note();
DROP TABLE link_orders_due;
DROP TABLE trade_settings;
DROP TABLE trade_orders;
ALTER TABLE purchase_orders DROP COLUMN short_at, DROP COLUMN cancel_asked_at, DROP COLUMN delivered_at, DROP COLUMN change,
  DROP COLUMN seller_note, DROP COLUMN seller_status, DROP COLUMN link_id;
DROP INDEX suppliers_link;
ALTER TABLE suppliers DROP COLUMN link_id;
