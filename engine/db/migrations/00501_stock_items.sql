-- Stock apart from products, S1a (docs/spec/what-a-business-buys.md §4,
-- §8; decision 0070). Taskiem is not in production: the model changes
-- outright, with nothing carried over.
--
-- - stock_items: what a business counts, in its scope. Its count is the
--   sum of its movements: it changes only when a movement is inserted, in
--   the movement's transaction (taskiem_stock_movement_moves), and a
--   trigger refuses any other change. At most 5,000 current ones per
--   business. group_of names the product a variant's stock item belongs to.
-- - product_stock: a catalogue row counted by a stock item, with how many
--   of it one sale uses; one per row.
-- - stock_movements are made again on stock items, and lose what sending a
--   change to the catalogue table needed (status, the claims, attempts,
--   errors): a movement is made with its count, in one transaction. The
--   outside reason goes with the outside check: the catalogue holds no
--   count.
-- - The catalogue no longer keeps stock, so storefronts.keeps_stock goes:
--   a catalogue with products counted by stock items is refreshed as one
--   keeping stock was (its deleted rows' links are dropped there), and the
--   evening total and the watch go to businesses with stock items.
-- +goose Up
CREATE TABLE stock_items (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  name          text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  unit          text NOT NULL DEFAULT '' CHECK (length(unit) <= 30),
  category      text NOT NULL DEFAULT '' CHECK (length(category) <= 60),
  count         bigint NOT NULL DEFAULT 0,
  cost_kobo     bigint CHECK (cost_kobo BETWEEN 0 AND 1000000000000),
  low_level     bigint CHECK (low_level BETWEEN 0 AND 1000000000000),
  target_level  bigint CHECK (target_level BETWEEN 0 AND 1000000000000),
  supplier_id   uuid REFERENCES suppliers(id),
  group_of      text CHECK (length(group_of) BETWEEN 1 AND 100),
  created_by    text NOT NULL CHECK (length(created_by) <= 200),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT clock_timestamp(),
  retired_at    timestamptz,
  mirrored_at   timestamptz,
  UNIQUE (tenant_id, id)
);
CREATE INDEX stock_items_tenant ON stock_items (tenant_id, lower(name)) WHERE retired_at IS NULL;
CREATE INDEX stock_items_group ON stock_items (tenant_id, group_of) WHERE group_of IS NOT NULL;
CREATE INDEX stock_items_unmirrored ON stock_items (tenant_id, updated_at) WHERE mirrored_at IS DISTINCT FROM updated_at;
ALTER TABLE stock_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_items TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON stock_items TO taskiem_app;
GRANT UPDATE (name, unit, category, count, cost_kobo, low_level, target_level, supplier_id, group_of, retired_at, mirrored_at) ON stock_items TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_stock_item_kept() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.count <> 0 THEN
      RAISE EXCEPTION 'a stock item starts at 0: its count is the sum of its movements' USING ERRCODE = 'check_violation';
    END IF;
    -- At most 5,000 current stock items per business, counted under a
    -- lock of the business's so two at once cannot pass it together.
    PERFORM pg_advisory_xact_lock(hashtextextended('stock_items ' || NEW.tenant_id::text, 0));
    IF (SELECT count(*) FROM stock_items WHERE tenant_id = NEW.tenant_id AND retired_at IS NULL) >= 5000 THEN
      RAISE EXCEPTION 'a business keeps at most 5,000 stock items' USING ERRCODE = 'check_violation';
    END IF;
    NEW.updated_at := clock_timestamp();
    RETURN NEW;
  END IF;
  -- The count changes only by a movement: from the movement's own
  -- trigger, never by a statement of its own.
  IF NEW.count IS DISTINCT FROM OLD.count AND pg_trigger_depth() < 2 THEN
    RAISE EXCEPTION 'a stock item''s count changes only by a movement' USING ERRCODE = 'check_violation';
  END IF;
  IF ROW(NEW.name, NEW.unit, NEW.category, NEW.count, NEW.cost_kobo, NEW.low_level, NEW.target_level, NEW.supplier_id, NEW.group_of, NEW.retired_at)
     IS DISTINCT FROM ROW(OLD.name, OLD.unit, OLD.category, OLD.count, OLD.cost_kobo, OLD.low_level, OLD.target_level, OLD.supplier_id, OLD.group_of, OLD.retired_at) THEN
    NEW.updated_at := clock_timestamp();
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER stock_item_kept BEFORE INSERT OR UPDATE ON stock_items
  FOR EACH ROW EXECUTE FUNCTION taskiem_stock_item_kept();

CREATE TABLE product_stock (
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  -- The catalogue row, in the owner's own table.
  product_id     text NOT NULL CHECK (length(product_id) BETWEEN 1 AND 100),
  stock_item_id  uuid NOT NULL,
  uses           bigint NOT NULL DEFAULT 1 CHECK (uses BETWEEN 1 AND 1000000),
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, product_id),
  FOREIGN KEY (tenant_id, stock_item_id) REFERENCES stock_items (tenant_id, id)
);
CREATE INDEX product_stock_item ON product_stock (tenant_id, stock_item_id);
ALTER TABLE product_stock ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_stock FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_stock TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON product_stock TO taskiem_app;
GRANT UPDATE (stock_item_id, uses) ON product_stock TO taskiem_app;

-- Movements, made again on stock items.
DROP FUNCTION taskiem_stock_due_tenants(timestamptz);
DROP TABLE stock_movements;
DROP FUNCTION taskiem_stock_movement_kept();
CREATE TABLE stock_movements (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  stock_item_id  uuid NOT NULL,
  -- The stock item's name when the movement was made.
  item           text NOT NULL CHECK (length(item) BETWEEN 1 AND 200),
  reason         text NOT NULL CHECK (reason IN ('order', 'sale', 'received', 'adjustment', 'count', 'moved')),
  delta          bigint,
  set_to         bigint CHECK (set_to >= 0),
  count_before   bigint NOT NULL,
  count_after    bigint NOT NULL,
  change         bigint NOT NULL,
  -- What a decrease asked for beyond what there was (a sale of more than
  -- the stock, an order short).
  short          bigint NOT NULL DEFAULT 0 CHECK (short >= 0),
  note           text CHECK (length(note) <= 200),
  -- What it came from: an order's reference, a sale, a purchase, a count.
  reference      text CHECK (length(reference) <= 100),
  actor          text NOT NULL CHECK (length(actor) <= 200),
  key            text NOT NULL CHECK (length(key) BETWEEN 1 AND 200),
  cost_kobo      bigint CHECK (cost_kobo >= 0),
  created_at     timestamptz NOT NULL DEFAULT now(),
  mirrored_at    timestamptz,
  UNIQUE (tenant_id, key),
  FOREIGN KEY (tenant_id, stock_item_id) REFERENCES stock_items (tenant_id, id),
  CHECK ((delta IS NULL) <> (set_to IS NULL)),
  CHECK (change = count_after - count_before)
);
CREATE INDEX stock_movements_item ON stock_movements (tenant_id, stock_item_id, created_at);
CREATE INDEX stock_movements_unmirrored ON stock_movements (tenant_id, created_at) WHERE mirrored_at IS NULL;
ALTER TABLE stock_movements ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_movements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_movements TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
-- A movement is a record: only when it was copied changes after.
GRANT SELECT, INSERT ON stock_movements TO taskiem_app;
GRANT UPDATE (mirrored_at) ON stock_movements TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_stock_movement_moves() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  -- The movement starts from the stock item's count and sets it, in the
  -- movement's transaction: so the count is always the sum of them.
  UPDATE stock_items SET count = NEW.count_after
   WHERE tenant_id = NEW.tenant_id AND id = NEW.stock_item_id AND count = NEW.count_before;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'a stock movement starts from its stock item''s count' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER stock_movement_moves AFTER INSERT ON stock_movements
  FOR EACH ROW EXECUTE FUNCTION taskiem_stock_movement_moves();

-- The sweep: movements and stock items to copy to the kept tables, and
-- sales waiting for their money. Tenants only.
CREATE POLICY dispatch ON stock_movements FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, reason, short, count_after, change, mirrored_at) ON stock_movements TO taskiem_dispatch;
CREATE POLICY dispatch ON stock_items FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, updated_at, mirrored_at, retired_at) ON stock_items TO taskiem_dispatch;
CREATE POLICY dispatch ON product_stock FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id) ON product_stock TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_stock_due_tenants(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT m.tenant_id FROM stock_movements m
    JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
   WHERE m.mirrored_at IS NULL AND m.created_at <= p_before
  UNION
  SELECT i.tenant_id FROM stock_items i
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE i.mirrored_at IS DISTINCT FROM i.updated_at
  UNION
  SELECT s.tenant_id FROM stock_sales s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.status = 'waiting'
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_stock_due_tenants(timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_stock_due_tenants(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_stock_due_tenants(timestamptz) TO taskiem_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_stock_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT i.tenant_id FROM stock_items i
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE i.retired_at IS NULL
$$;
CREATE OR REPLACE FUNCTION taskiem_storefronts_due(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE (s.status IN ('published', 'closed') OR (s.catalogue_table IS NOT NULL AND EXISTS (SELECT 1 FROM product_stock p WHERE p.tenant_id = s.tenant_id)))
     AND COALESCE(s.catalogue_synced_at, '-infinity') <= p_before
$$;
CREATE OR REPLACE FUNCTION taskiem_money_leak_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM money_entries UNION SELECT tenant_id FROM customer_invoices WHERE sent_at IS NOT NULL
  UNION SELECT tenant_id FROM stock_movements
   WHERE short > 0 OR count_after < 0 OR (reason = 'count' AND change < 0)
  UNION SELECT tenant_id FROM bills WHERE NOT voided AND amount_kobo > paid_kobo
$$;
-- +goose StatementEnd
GRANT SELECT (catalogue_table) ON storefronts TO taskiem_dispatch;
ALTER TABLE storefronts DROP COLUMN keeps_stock;

-- +goose Down
ALTER TABLE storefronts ADD COLUMN keeps_stock boolean
  GENERATED ALWAYS AS (catalogue_table IS NOT NULL AND COALESCE(catalogue_columns->>'stock', '') <> '') STORED;
GRANT SELECT (keeps_stock) ON storefronts TO taskiem_dispatch;
REVOKE SELECT (catalogue_table) ON storefronts FROM taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefronts_due(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE (s.status IN ('published', 'closed') OR s.keeps_stock) AND COALESCE(s.catalogue_synced_at, '-infinity') <= p_before
$$;
CREATE OR REPLACE FUNCTION taskiem_stock_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.keeps_stock
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_stock_due_tenants(timestamptz);
DROP TABLE stock_movements;
DROP FUNCTION taskiem_stock_movement_moves();
CREATE TABLE stock_movements (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  item_id       text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100),
  item          text NOT NULL CHECK (length(item) BETWEEN 1 AND 200),
  reason        text NOT NULL CHECK (reason IN ('order', 'sale', 'received', 'adjustment', 'count', 'outside', 'moved')),
  delta         bigint,
  set_to        bigint CHECK (set_to >= 0),
  status        text NOT NULL CHECK (status IN ('pending', 'done', 'void')),
  count_before  bigint,
  count_after   bigint,
  change        bigint,
  short         bigint NOT NULL DEFAULT 0 CHECK (short >= 0),
  note          text CHECK (length(note) <= 200),
  reference     text CHECK (length(reference) <= 100),
  actor         text NOT NULL CHECK (length(actor) <= 200),
  key           text NOT NULL CHECK (length(key) BETWEEN 1 AND 200),
  attempts      int NOT NULL DEFAULT 0,
  error         text CHECK (length(error) <= 300),
  created_at    timestamptz NOT NULL DEFAULT now(),
  claimed_at    timestamptz,
  done_at       timestamptz,
  mirrored_at   timestamptz,
  cost_kobo     bigint CHECK (cost_kobo >= 0),
  claims        int NOT NULL DEFAULT 0,
  UNIQUE (tenant_id, key),
  CHECK ((delta IS NULL) <> (set_to IS NULL)),
  CHECK (status <> 'done' OR (count_before IS NOT NULL AND count_after IS NOT NULL AND change = count_after - count_before AND done_at IS NOT NULL))
);
CREATE INDEX stock_movements_item ON stock_movements (tenant_id, item_id, created_at);
CREATE INDEX stock_movements_pending ON stock_movements (tenant_id, created_at) WHERE status = 'pending';
CREATE INDEX stock_movements_unmirrored ON stock_movements (tenant_id, done_at) WHERE status = 'done' AND mirrored_at IS NULL;
ALTER TABLE stock_movements ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_movements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_movements TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON stock_movements TO taskiem_app;
GRANT UPDATE (status, count_before, count_after, change, short, attempts, error, claimed_at, done_at, mirrored_at, claims) ON stock_movements TO taskiem_app;
-- +goose StatementBegin
CREATE FUNCTION taskiem_stock_movement_kept() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status IN ('done', 'void') AND (NEW.status IS DISTINCT FROM OLD.status OR NEW.count_before IS DISTINCT FROM OLD.count_before
      OR NEW.count_after IS DISTINCT FROM OLD.count_after OR NEW.change IS DISTINCT FROM OLD.change OR NEW.short IS DISTINCT FROM OLD.short) THEN
    RAISE EXCEPTION 'a stock movement that is done or void is not changed' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER stock_movement_kept BEFORE UPDATE ON stock_movements
  FOR EACH ROW EXECUTE FUNCTION taskiem_stock_movement_kept();
CREATE POLICY dispatch ON stock_movements FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, status, claimed_at, mirrored_at, reason, short, count_after, change) ON stock_movements TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_stock_due_tenants(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT m.tenant_id FROM stock_movements m
    JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
   WHERE (m.status = 'pending' AND COALESCE(m.claimed_at, '-infinity') <= p_before)
      OR (m.status = 'done' AND m.mirrored_at IS NULL)
  UNION
  SELECT DISTINCT s.tenant_id FROM stock_sales s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.status = 'waiting'
$$;
CREATE OR REPLACE FUNCTION taskiem_money_leak_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM money_entries UNION SELECT tenant_id FROM customer_invoices WHERE sent_at IS NOT NULL
  UNION SELECT tenant_id FROM stock_movements
   WHERE status = 'done' AND (short > 0 OR count_after < 0 OR (reason = 'count' AND change < 0))
  UNION SELECT tenant_id FROM bills WHERE NOT voided AND amount_kobo > paid_kobo
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_stock_due_tenants(timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_stock_due_tenants(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_stock_due_tenants(timestamptz) TO taskiem_app;
DROP TABLE product_stock;
DROP TABLE stock_items;
DROP FUNCTION taskiem_stock_item_kept();
