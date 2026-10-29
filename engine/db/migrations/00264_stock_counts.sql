-- Counts, shrinkage and leaks (docs/spec/stock.md §7, K4).
--
-- A count is what was on the shelf, item by item, against what Taskiem
-- counted; each difference is a count movement naming it. A count of every
-- item closes a counting period.
--
-- A movement keeps the item's cost price when it was made, so what was
-- lost is valued at cost. Three stock leaks are money (money_leaks): a
-- sale of more than the stock, a count below zero in the table, and a
-- count's loss above the business's limit. Two owner alerts are not: an
-- item low for a week with nothing ordered, and an order confirmed and
-- not received after its expected days (purchase_orders.expected_days).

-- +goose Up
ALTER TABLE stock_movements ADD COLUMN cost_kobo bigint CHECK (cost_kobo >= 0);

CREATE TABLE stock_counts (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  reference   text NOT NULL CHECK (reference ~ '^C-[A-Z0-9]{6}$'),
  -- [{item_id, item, unit, expected, counted, cost_kobo}]
  lines       jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array' AND jsonb_array_length(lines) BETWEEN 1 AND 1000),
  -- Every item was counted: the counting period closes.
  full_count  boolean NOT NULL,
  reason      text NOT NULL DEFAULT '' CHECK (length(reason) <= 200),
  lost_kobo   bigint NOT NULL DEFAULT 0 CHECK (lost_kobo >= 0),
  actor       text NOT NULL,
  key         text NOT NULL,
  counted_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, reference),
  UNIQUE (tenant_id, key)
);
CREATE INDEX stock_counts_recent ON stock_counts (tenant_id, counted_at DESC);
ALTER TABLE stock_counts ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_counts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_counts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON stock_counts TO taskiem_app;

-- A count's loss at cost above this is a leak (NULL: ₦5,000).
ALTER TABLE stock_settings ADD COLUMN count_limit_kobo bigint CHECK (count_limit_kobo >= 0);

-- When an order is expected, once confirmed.
ALTER TABLE purchase_orders ADD COLUMN expected_days int NOT NULL DEFAULT 3 CHECK (expected_days BETWEEN 1 AND 90);

-- Items low on stock, since when: kept by the stock watch from the
-- catalogue, for the owner's alert when one stays low a week with nothing
-- ordered.
CREATE TABLE stock_low (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  item_id     text NOT NULL CHECK (length(item_id) <= 64),
  item        text NOT NULL CHECK (length(item) <= 200),
  since       timestamptz NOT NULL DEFAULT now(),
  alerted_at  timestamptz,
  PRIMARY KEY (tenant_id, item_id)
);
ALTER TABLE stock_low ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_low FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_low TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON stock_low TO taskiem_app;

-- The three stock leaks.
ALTER TABLE money_leaks DROP CONSTRAINT money_leaks_kind_check;
ALTER TABLE money_leaks ADD CONSTRAINT money_leaks_kind_check CHECK (kind IN ('unmatched', 'overdue', 'duplicate', 'overpaid', 'void_paid', 'fee', 'rider_owes',
  'stock_short', 'stock_negative', 'stock_count'));

-- The leak detector looks at businesses whose movements may be leaks too.
GRANT SELECT (reason, short, count_after, change) ON stock_movements TO taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_leak_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM money_entries UNION SELECT tenant_id FROM customer_invoices WHERE sent_at IS NOT NULL
  UNION SELECT tenant_id FROM stock_movements
   WHERE status = 'done' AND (short > 0 OR count_after < 0 OR (reason = 'count' AND change < 0))
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_leak_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM money_entries UNION SELECT tenant_id FROM customer_invoices WHERE sent_at IS NOT NULL
$$;
-- +goose StatementEnd
REVOKE SELECT (reason, short, count_after, change) ON stock_movements FROM taskiem_dispatch;
DELETE FROM money_leaks WHERE kind IN ('stock_short', 'stock_negative', 'stock_count');
ALTER TABLE money_leaks DROP CONSTRAINT money_leaks_kind_check;
ALTER TABLE money_leaks ADD CONSTRAINT money_leaks_kind_check CHECK (kind IN ('unmatched', 'overdue', 'duplicate', 'overpaid', 'void_paid', 'fee', 'rider_owes'));
DROP TABLE stock_low;
ALTER TABLE purchase_orders DROP COLUMN expected_days;
ALTER TABLE stock_settings DROP COLUMN count_limit_kobo;
DROP TABLE stock_counts;
ALTER TABLE stock_movements DROP COLUMN cost_kobo;
