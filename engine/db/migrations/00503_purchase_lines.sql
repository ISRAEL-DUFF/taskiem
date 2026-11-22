-- Purchases on the server, U1 (docs/spec/what-a-business-buys.md §5, §6,
-- §8; decision 0070). Taskiem is not in production: the model changes
-- outright, with nothing carried over.
--
-- - spend_labels: the business's own labels, made by typing them. A name
--   is unique per business without case, merged ones included (typing a
--   merged label's name finds it, and so the label it was merged into).
--   A merge is one step: a label merged into another names one that is
--   not itself merged, and a label others are merged into is not merged
--   itself (taskiem_spend_label_merge). Labels are never deleted.
-- - purchase_lines: an order's lines, a table of their own in place of
--   purchase_orders.lines, so each is treated (record, stock, track),
--   received and labelled on its own. A stock line names its stock item;
--   a line's treatment, stock item and uses are locked once anything on
--   it is received, and such a line is not deleted
--   (taskiem_purchase_line_kept). At most 50 to an order (position 0 to 49).
--   purchase_orders.change and .short stay: the seller's answer and our
--   count of what came, each by the line's position.
-- - item_links: made again as the buyer's memory of a linked supplier's
--   item (§6): what to do with it next time. The buyer's own catalogue
--   row (item_id) goes: a line is the supplier's item, treated, not one
--   of the buyer's products.
-- - supplier_items: the same memory for a supplier not on Taskiem, by the
--   item's name as typed (name_key: lowered, spaces trimmed and single).
-- - money_entries.label_id: an expense's label (none: Not labelled). An
--   entry is still never changed: taskiem_money_entry_kept now refuses
--   any change but marking it copied, label_id included.
-- - kept_things: Things we have. Receiving a tracked line writes one row
--   per unit, numbered on the line (unit_no), so a repeated receipt makes
--   nothing twice.
-- +goose Up
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_tenant_id_id_key UNIQUE (tenant_id, id);
ALTER TABLE suppliers ADD CONSTRAINT suppliers_tenant_id_id_key UNIQUE (tenant_id, id);

CREATE TABLE spend_labels (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 60 AND name = btrim(name)),
  merged_into  uuid,
  created_by   text NOT NULL CHECK (length(created_by) <= 200),
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, merged_into) REFERENCES spend_labels (tenant_id, id),
  CHECK (merged_into <> id)
);
CREATE UNIQUE INDEX spend_labels_name ON spend_labels (tenant_id, lower(name));
CREATE INDEX spend_labels_merged ON spend_labels (tenant_id, merged_into) WHERE merged_into IS NOT NULL;
ALTER TABLE spend_labels ENABLE ROW LEVEL SECURITY;
ALTER TABLE spend_labels FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON spend_labels TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON spend_labels TO taskiem_app;
GRANT UPDATE (name, merged_into) ON spend_labels TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_spend_label_merge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.merged_into IS NULL OR (TG_OP = 'UPDATE' AND NEW.merged_into IS NOT DISTINCT FROM OLD.merged_into) THEN
    RETURN NEW;
  END IF;
  -- The label merged into is locked, so a merge of it at the same time
  -- waits and then sees this one (and is refused), or is seen here.
  IF EXISTS (SELECT 1 FROM spend_labels WHERE tenant_id = NEW.tenant_id AND id = NEW.merged_into AND merged_into IS NOT NULL FOR SHARE) THEN
    RAISE EXCEPTION 'a label is merged into one that is not itself merged' USING ERRCODE = 'check_violation';
  END IF;
  IF EXISTS (SELECT 1 FROM spend_labels WHERE tenant_id = NEW.tenant_id AND merged_into = NEW.id) THEN
    RAISE EXCEPTION 'a label others are merged into is not merged itself: move them first' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER spend_label_merge BEFORE INSERT OR UPDATE ON spend_labels
  FOR EACH ROW EXECUTE FUNCTION taskiem_spend_label_merge();

-- Every expense carries its label, or none (Not labelled).
ALTER TABLE money_entries ADD CONSTRAINT money_entries_tenant_id_id_key UNIQUE (tenant_id, id);
ALTER TABLE money_entries ADD COLUMN label_id uuid;
ALTER TABLE money_entries ADD CONSTRAINT money_entries_label FOREIGN KEY (tenant_id, label_id) REFERENCES spend_labels (tenant_id, id);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_entry_kept() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.mirrored_at IS NOT NULL OR NEW.mirrored_at IS NULL
     OR (to_jsonb(NEW) - 'mirrored_at') IS DISTINCT FROM (to_jsonb(OLD) - 'mirrored_at') THEN
    RAISE EXCEPTION 'a money log entry is never changed' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- The treatments of a line, and of the memory of an item.
CREATE TABLE purchase_lines (
  id                 uuid PRIMARY KEY,
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  purchase_order_id  uuid NOT NULL,
  position           int NOT NULL CHECK (position BETWEEN 0 AND 49),
  name               text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  unit               text NOT NULL DEFAULT '' CHECK (length(unit) <= 40),
  quantity           bigint NOT NULL CHECK (quantity BETWEEN 1 AND 1000000),
  -- The price of one.
  cost_kobo          bigint NOT NULL CHECK (cost_kobo BETWEEN 0 AND 10000000000),
  received           bigint NOT NULL DEFAULT 0 CHECK (received >= 0),
  -- What a linked supplier says it delivered, mirrored from its order.
  delivered          bigint CHECK (delivered >= 0),
  treatment          text NOT NULL DEFAULT 'record' CHECK (treatment IN ('record', 'stock', 'track')),
  -- For a stock line: the stock item receiving moves, and how many of it
  -- one of the line is.
  stock_item_id      uuid,
  uses               bigint NOT NULL DEFAULT 1 CHECK (uses BETWEEN 1 AND 1000000),
  label_id           uuid,
  -- The linked supplier's own item, on an order to a linked supplier.
  seller_item_id     text CHECK (length(seller_item_id) BETWEEN 1 AND 100),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, purchase_order_id, position),
  FOREIGN KEY (tenant_id, purchase_order_id) REFERENCES purchase_orders (tenant_id, id),
  FOREIGN KEY (tenant_id, stock_item_id) REFERENCES stock_items (tenant_id, id),
  FOREIGN KEY (tenant_id, label_id) REFERENCES spend_labels (tenant_id, id),
  CHECK ((treatment = 'stock') = (stock_item_id IS NOT NULL)),
  CHECK (treatment = 'stock' OR uses = 1),
  -- A line over 200 is not tracked one by one (§5.3).
  CHECK (treatment <> 'track' OR quantity <= 200)
);
CREATE INDEX purchase_lines_stock_item ON purchase_lines (tenant_id, stock_item_id) WHERE stock_item_id IS NOT NULL;
CREATE INDEX purchase_lines_seller_item ON purchase_lines (tenant_id, seller_item_id) WHERE seller_item_id IS NOT NULL;
ALTER TABLE purchase_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_lines TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON purchase_lines TO taskiem_app;
GRANT UPDATE (position, name, unit, quantity, cost_kobo, received, delivered, treatment, stock_item_id, uses, label_id, seller_item_id)
  ON purchase_lines TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_purchase_line_kept() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  -- Once anything on a line is received, undoing its treatment would move
  -- stock back or remove rows someone may have changed (§6).
  IF TG_OP = 'DELETE' THEN
    IF OLD.received > 0 THEN
      RAISE EXCEPTION 'a purchase line with something received is not removed' USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.received > 0 AND ROW(NEW.treatment, NEW.stock_item_id, NEW.uses) IS DISTINCT FROM ROW(OLD.treatment, OLD.stock_item_id, OLD.uses) THEN
    RAISE EXCEPTION 'a purchase line''s treatment is locked once anything on it is received' USING ERRCODE = 'check_violation';
  END IF;
  NEW.updated_at := now();
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER purchase_line_kept BEFORE UPDATE OR DELETE ON purchase_lines
  FOR EACH ROW EXECUTE FUNCTION taskiem_purchase_line_kept();

ALTER TABLE purchase_orders DROP COLUMN lines;

-- The buyer's memory of a linked supplier's item.
DROP TABLE item_links;
CREATE TABLE item_links (
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  link_id         uuid NOT NULL REFERENCES business_links(id),
  seller_item_id  text NOT NULL CHECK (length(seller_item_id) BETWEEN 1 AND 100),
  -- The supplier's name for it when last set, for the Suppliers page.
  seller_item     text NOT NULL CHECK (length(seller_item) BETWEEN 1 AND 120),
  treatment       text NOT NULL CHECK (treatment IN ('record', 'stock', 'track')),
  stock_item_id   uuid,
  uses            bigint NOT NULL DEFAULT 1 CHECK (uses BETWEEN 1 AND 1000000),
  label_id        uuid,
  updated_by      text NOT NULL CHECK (length(updated_by) <= 200),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id, seller_item_id),
  FOREIGN KEY (tenant_id, stock_item_id) REFERENCES stock_items (tenant_id, id),
  FOREIGN KEY (tenant_id, label_id) REFERENCES spend_labels (tenant_id, id),
  CHECK ((treatment = 'stock') = (stock_item_id IS NOT NULL)),
  CHECK (treatment = 'stock' OR uses = 1)
);
CREATE INDEX item_links_stock_item ON item_links (tenant_id, stock_item_id) WHERE stock_item_id IS NOT NULL;
ALTER TABLE item_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE item_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON item_links TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON item_links TO taskiem_app;

-- The same memory for a supplier not on Taskiem.
CREATE TABLE supplier_items (
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  supplier_id    uuid NOT NULL,
  name_key       text NOT NULL CHECK (length(name_key) BETWEEN 1 AND 200
                   AND name_key = lower(regexp_replace(btrim(name_key), '\s+', ' ', 'g'))),
  -- The name as last typed, for the Suppliers page.
  name           text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  treatment      text NOT NULL CHECK (treatment IN ('record', 'stock', 'track')),
  stock_item_id  uuid,
  uses           bigint NOT NULL DEFAULT 1 CHECK (uses BETWEEN 1 AND 1000000),
  label_id       uuid,
  updated_by     text NOT NULL CHECK (length(updated_by) <= 200),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, supplier_id, name_key),
  FOREIGN KEY (tenant_id, supplier_id) REFERENCES suppliers (tenant_id, id),
  FOREIGN KEY (tenant_id, stock_item_id) REFERENCES stock_items (tenant_id, id),
  FOREIGN KEY (tenant_id, label_id) REFERENCES spend_labels (tenant_id, id),
  CHECK ((treatment = 'stock') = (stock_item_id IS NOT NULL)),
  CHECK (treatment = 'stock' OR uses = 1)
);
CREATE INDEX supplier_items_stock_item ON supplier_items (tenant_id, stock_item_id) WHERE stock_item_id IS NOT NULL;
ALTER TABLE supplier_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON supplier_items TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON supplier_items TO taskiem_app;

-- Things we have.
CREATE TABLE kept_things (
  id                 uuid PRIMARY KEY,
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  name               text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  label_id           uuid,
  -- From whom and on what order; none when added by hand.
  supplier_id        uuid,
  purchase_order_id  uuid,
  purchase_line_id   uuid,
  -- Which of the line's units it is, from 1: receiving writes each once.
  unit_no            int CHECK (unit_no BETWEEN 1 AND 200),
  cost_kobo          bigint CHECK (cost_kobo BETWEEN 0 AND 10000000000),
  received_at        timestamptz,
  serial             text CHECK (length(serial) <= 100),
  warranty_until     date,
  note               text CHECK (length(note) <= 500),
  status             text NOT NULL DEFAULT 'have' CHECK (status IN ('have', 'sold', 'broken', 'given', 'gone')),
  status_at          timestamptz NOT NULL DEFAULT now(),
  -- The money in when it was sold with a price (Sale of things we had).
  sale_entry_id      uuid,
  created_by         text NOT NULL CHECK (length(created_by) <= 200),
  created_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, purchase_line_id, unit_no),
  FOREIGN KEY (tenant_id, label_id) REFERENCES spend_labels (tenant_id, id),
  FOREIGN KEY (tenant_id, supplier_id) REFERENCES suppliers (tenant_id, id),
  FOREIGN KEY (tenant_id, purchase_order_id) REFERENCES purchase_orders (tenant_id, id),
  FOREIGN KEY (tenant_id, purchase_line_id) REFERENCES purchase_lines (tenant_id, id),
  FOREIGN KEY (tenant_id, sale_entry_id) REFERENCES money_entries (tenant_id, id),
  CHECK ((purchase_line_id IS NULL) = (unit_no IS NULL)),
  CHECK (purchase_line_id IS NULL OR purchase_order_id IS NOT NULL)
);
CREATE INDEX kept_things_list ON kept_things (tenant_id, created_at DESC);
CREATE INDEX kept_things_order ON kept_things (tenant_id, purchase_order_id) WHERE purchase_order_id IS NOT NULL;
ALTER TABLE kept_things ENABLE ROW LEVEL SECURITY;
ALTER TABLE kept_things FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kept_things TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON kept_things TO taskiem_app;
GRANT UPDATE (name, label_id, cost_kobo, serial, warranty_until, note, status, sale_entry_id) ON kept_things TO taskiem_app;
-- +goose StatementBegin
CREATE FUNCTION taskiem_kept_thing_status() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status IS DISTINCT FROM OLD.status THEN
    NEW.status_at := now();
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER kept_thing_status BEFORE UPDATE ON kept_things
  FOR EACH ROW EXECUTE FUNCTION taskiem_kept_thing_status();

-- +goose Down
DROP TABLE kept_things;
DROP FUNCTION taskiem_kept_thing_status();
DROP TABLE supplier_items;
DROP TABLE item_links;
CREATE TABLE item_links (
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  link_id         uuid NOT NULL REFERENCES business_links(id),
  item_id         text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100),
  seller_item_id  text NOT NULL CHECK (length(seller_item_id) BETWEEN 1 AND 100),
  seller_item     text NOT NULL CHECK (length(seller_item) BETWEEN 1 AND 120),
  updated_by      text NOT NULL,
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id, item_id),
  UNIQUE (tenant_id, link_id, seller_item_id)
);
ALTER TABLE item_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE item_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON item_links TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON item_links TO taskiem_app;
-- The lines go back into their order, as the JSON 00263 and S1b kept.
ALTER TABLE purchase_orders ADD COLUMN lines jsonb;
UPDATE purchase_orders o SET lines = COALESCE((SELECT jsonb_agg(jsonb_strip_nulls(jsonb_build_object(
    'item_id', COALESCE(l.stock_item_id::text, l.seller_item_id, l.id::text), 'item', l.name, 'unit', NULLIF(l.unit, ''), 'quantity', l.quantity,
    'cost_kobo', l.cost_kobo, 'received', l.received, 'delivered', l.delivered, 'stock_item_id', l.stock_item_id,
    'uses', CASE WHEN l.stock_item_id IS NOT NULL THEN l.uses END)) ORDER BY l.position)
  FROM purchase_lines l WHERE l.tenant_id = o.tenant_id AND l.purchase_order_id = o.id), '[]');
ALTER TABLE purchase_orders ALTER COLUMN lines SET NOT NULL;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_lines_check
  CHECK (jsonb_typeof(lines) = 'array' AND jsonb_array_length(lines) BETWEEN 1 AND 50) NOT VALID;
DROP TABLE purchase_lines;
DROP FUNCTION taskiem_purchase_line_kept();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_entry_kept() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.mirrored_at IS NOT NULL OR NEW.mirrored_at IS NULL THEN
    RAISE EXCEPTION 'a money log entry is never changed' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER TABLE money_entries DROP COLUMN label_id;
ALTER TABLE money_entries DROP CONSTRAINT money_entries_tenant_id_id_key;
DROP TABLE spend_labels;
DROP FUNCTION taskiem_spend_label_merge();
ALTER TABLE suppliers DROP CONSTRAINT suppliers_tenant_id_id_key;
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_tenant_id_id_key;
