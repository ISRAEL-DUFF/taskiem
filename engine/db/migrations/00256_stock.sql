-- Stock by chat K1 (docs/spec/stock.md §4, decision 0057): every change to
-- an item's stock is a movement Taskiem records here, in the transaction
-- of what caused it, before the change is sent to the catalogue table in
-- the business's Xenibase project.
--
-- A movement asks for a change (delta, never taking the count below zero)
-- or, for a count, a count (set_to). It is pending until the table holds
-- it: the count read (count_before) and the count sent (count_after) are
-- recorded before the change is sent, under the movement's key, and the
-- change is made only while the table still holds count_before. Done, its
-- change is count_after - count_before, so an item's count is the sum of
-- its done movements' changes. A count the table holds that Taskiem did
-- not make is recorded as an 'outside' movement. A movement for an item
-- that is gone, or a catalogue that tracks no stock, is void.

-- +goose Up
CREATE TABLE stock_movements (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  -- The catalogue row and its name when the movement was made.
  item_id       text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100),
  item          text NOT NULL CHECK (length(item) BETWEEN 1 AND 200),
  reason        text NOT NULL CHECK (reason IN ('order', 'sale', 'received', 'adjustment', 'count', 'outside')),
  delta         bigint,
  set_to        bigint CHECK (set_to >= 0),
  status        text NOT NULL CHECK (status IN ('pending', 'done', 'void')),
  count_before  bigint,
  count_after   bigint,
  change        bigint,
  -- What a decrease asked for beyond what there was (a sale of more than
  -- the stock, an order short).
  short         bigint NOT NULL DEFAULT 0 CHECK (short >= 0),
  note          text CHECK (length(note) <= 200),
  -- What it came from: an order's reference, a sale, a purchase, a count.
  reference     text CHECK (length(reference) <= 100),
  actor         text NOT NULL CHECK (length(actor) <= 200),
  key           text NOT NULL CHECK (length(key) BETWEEN 1 AND 200),
  attempts      int NOT NULL DEFAULT 0,
  error         text CHECK (length(error) <= 300),
  created_at    timestamptz NOT NULL DEFAULT now(),
  claimed_at    timestamptz,
  done_at       timestamptz,
  mirrored_at   timestamptz,
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
-- Movements are a record: what changes after one is made is its progress,
-- never what was asked or why.
GRANT SELECT, INSERT ON stock_movements TO taskiem_app;
GRANT UPDATE (status, count_before, count_after, change, short, attempts, error, claimed_at, done_at, mirrored_at) ON stock_movements TO taskiem_app;

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

-- The sweep: movements waiting to be sent, or to be copied to the kept
-- table. Tenants only.
CREATE POLICY dispatch ON stock_movements FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, status, claimed_at, mirrored_at) ON stock_movements TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_stock_due_tenants(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT m.tenant_id FROM stock_movements m
    JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
   WHERE (m.status = 'pending' AND COALESCE(m.claimed_at, '-infinity') <= p_before)
      OR (m.status = 'done' AND m.mirrored_at IS NULL)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_stock_due_tenants(timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_stock_due_tenants(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_stock_due_tenants(timestamptz) TO taskiem_app;

-- A catalogue that tracks stock is refreshed every 15 minutes whether or
-- not its page is published, so a count changed in the table is found:
-- a shop keeping stock needs no published page.
ALTER TABLE storefronts ADD COLUMN keeps_stock boolean
  GENERATED ALWAYS AS (catalogue_table IS NOT NULL AND COALESCE(catalogue_columns->>'stock', '') <> '') STORED;
GRANT SELECT (keeps_stock) ON storefronts TO taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefronts_due(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE (s.status IN ('published', 'closed') OR s.keeps_stock) AND COALESCE(s.catalogue_synced_at, '-infinity') <= p_before
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefronts_due(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.status IN ('published', 'closed') AND COALESCE(s.catalogue_synced_at, '-infinity') <= p_before
$$;
-- +goose StatementEnd
REVOKE SELECT (keeps_stock) ON storefronts FROM taskiem_dispatch;
ALTER TABLE storefronts DROP COLUMN keeps_stock;
DROP FUNCTION taskiem_stock_due_tenants(timestamptz);
DROP TABLE stock_movements;
DROP FUNCTION taskiem_stock_movement_kept();
