-- Stock by chat K2 (docs/spec/stock.md §5, decision 0057): a sale at the
-- counter, recorded by chat, voice note or the web, moves stock and money
-- together. A sale is recorded with its movements (reason sale) and, for
-- cash, its takings entry in the money log, in one transaction. A sale
-- paid by transfer or on the terminal waits for its money: the money the
-- rail reports (a transfer in, a terminal's sale) is matched to it once,
-- and an entry counts for one sale at most. A terminal's sale with no sale
-- recorded can be given its items afterwards (matched by the owner).
--
-- The evening total goes to the owners in chat once a day, after the
-- business closes (money_digests' new kind, the outbox's new kind).

-- +goose Up
CREATE TABLE stock_sales (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  -- Short, for people and for a transfer's narration: S-7QX4KD.
  reference    text NOT NULL CHECK (reference ~ '^S-[A-Z0-9]{6}$'),
  -- [{item_id, item, quantity, unit, price_kobo, line_kobo}]
  lines        jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array' AND jsonb_array_length(lines) BETWEEN 1 AND 50),
  amount_kobo  bigint NOT NULL CHECK (amount_kobo >= 0 AND amount_kobo <= 100000000000),
  paid_how     text NOT NULL CHECK (paid_how IN ('cash', 'transfer', 'pos')),
  -- recorded: cash, its takings entry written; waiting: transfer or POS,
  -- its money not yet seen; matched: its money matched to it.
  status       text NOT NULL CHECK (status IN ('recorded', 'waiting', 'matched')),
  entry_id     uuid REFERENCES money_entries(id),
  matched_how  text CHECK (matched_how IN ('reference', 'amount', 'owner')),
  matched_at   timestamptz,
  note         text CHECK (length(note) <= 200),
  actor        text NOT NULL CHECK (length(actor) <= 200),
  key          text NOT NULL CHECK (length(key) BETWEEN 1 AND 200),
  sold_at      timestamptz NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, key),
  UNIQUE (tenant_id, reference),
  CHECK ((status = 'waiting') = (entry_id IS NULL)),
  CHECK ((status = 'matched') = (matched_how IS NOT NULL AND matched_at IS NOT NULL)),
  CHECK (status <> 'recorded' OR paid_how = 'cash')
);
-- An entry counts for one sale.
CREATE UNIQUE INDEX stock_sales_entry ON stock_sales (entry_id) WHERE entry_id IS NOT NULL;
CREATE INDEX stock_sales_day ON stock_sales (tenant_id, sold_at);
CREATE INDEX stock_sales_waiting ON stock_sales (tenant_id, sold_at) WHERE status = 'waiting';

ALTER TABLE stock_sales ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_sales FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_sales TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON stock_sales TO taskiem_app;
GRANT UPDATE (status, entry_id, matched_how, matched_at) ON stock_sales TO taskiem_app;

-- A sale is a record: once its money is matched, nothing about it changes;
-- before, only its match may be written.
-- +goose StatementBegin
CREATE FUNCTION taskiem_stock_sale_kept() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'waiting' OR NEW.status <> 'matched' THEN
    RAISE EXCEPTION 'a sale is never changed; correct the stock with an adjustment' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER stock_sale_kept BEFORE UPDATE ON stock_sales
  FOR EACH ROW EXECUTE FUNCTION taskiem_stock_sale_kept();

-- The sweep matches waiting sales; the evening total goes to businesses
-- keeping stock.
CREATE POLICY dispatch ON stock_sales FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, status) ON stock_sales TO taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_stock_due_tenants(p_before timestamptz)
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
CREATE FUNCTION taskiem_stock_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.keeps_stock
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_stock_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_stock_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_stock_tenants() TO taskiem_app;

ALTER TABLE money_digests DROP CONSTRAINT money_digests_kind_check;
ALTER TABLE money_digests ADD CONSTRAINT money_digests_kind_check CHECK (kind IN ('daily', 'monthly', 'sales_evening'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family', 'rider')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind IN ('money_daily', 'sales_evening')) = (digest_id IS NOT NULL)));

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind = 'sales_evening';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family', 'rider')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind = 'money_daily') = (digest_id IS NOT NULL)));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider'));
DELETE FROM money_digests WHERE kind = 'sales_evening';
ALTER TABLE money_digests DROP CONSTRAINT money_digests_kind_check;
ALTER TABLE money_digests ADD CONSTRAINT money_digests_kind_check CHECK (kind IN ('daily', 'monthly'));
DROP FUNCTION taskiem_stock_tenants();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_stock_due_tenants(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT m.tenant_id FROM stock_movements m
    JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
   WHERE (m.status = 'pending' AND COALESCE(m.claimed_at, '-infinity') <= p_before)
      OR (m.status = 'done' AND m.mirrored_at IS NULL)
$$;
-- +goose StatementEnd
DROP TABLE stock_sales;
DROP FUNCTION taskiem_stock_sale_kept();
