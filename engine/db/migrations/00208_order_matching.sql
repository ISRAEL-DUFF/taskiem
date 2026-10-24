-- Matching money to storefront orders (spec invoices-and-money-log 8.1 to
-- 8.3; docs/matching.md, docs/storefronts.md#payments-and-matching).
--
-- A payment counted towards an order is a row of storefront_order_payments,
-- as one counted towards an invoice is a row of customer_invoice_payments:
-- inserted once, undone by a row naming it, copied to the kept table
-- money_matches as order:<reference>. One money log entry counts towards
-- one invoice or order at a time, and an order is paid by one payment at a
-- time: both checked by the writer under locks (an undone match stays, so
-- no index can say it). Suggestions name an invoice or an order.
-- +goose Up
CREATE TABLE storefront_order_payments (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  order_id     uuid NOT NULL REFERENCES storefront_orders(id),
  entry_id     uuid NOT NULL REFERENCES money_entries(id),
  amount_kobo  bigint NOT NULL CHECK (amount_kobo > 0),
  how          text NOT NULL CHECK (how IN ('exact', 'suggested', 'suggested_auto', 'manual')),
  by           text NOT NULL,
  reasons      jsonb NOT NULL DEFAULT '[]',
  -- The order's status before this payment moved it to paid; NULL when
  -- the order was already paid (its own payment check came first).
  from_status  text,
  -- Whether marking it paid started or woke the order's run: such a
  -- payment cannot be undone (the order went on to be fulfilled).
  woke_run     boolean NOT NULL DEFAULT false,
  undoes       uuid UNIQUE REFERENCES storefront_order_payments(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  mirrored_at  timestamptz
);
CREATE INDEX storefront_order_payments_order ON storefront_order_payments (order_id);
CREATE INDEX storefront_order_payments_entry ON storefront_order_payments (entry_id);
CREATE INDEX storefront_order_payments_unmirrored ON storefront_order_payments (created_at) WHERE mirrored_at IS NULL;
ALTER TABLE storefront_order_payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_order_payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_order_payments TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON storefront_order_payments TO taskiem_app;
GRANT UPDATE (mirrored_at) ON storefront_order_payments TO taskiem_app;
CREATE TRIGGER order_payment_kept BEFORE UPDATE ON storefront_order_payments
  FOR EACH ROW EXECUTE FUNCTION taskiem_invoice_payment_kept();
CREATE POLICY dispatch ON storefront_order_payments FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON storefront_order_payments TO taskiem_dispatch;
-- Exact matching finds an order by its checkout page's reference.
CREATE INDEX storefront_orders_payment_ref ON storefront_orders (tenant_id, payment_ref) WHERE payment_ref IS NOT NULL;

-- A suggestion names an invoice or an order.
ALTER TABLE money_suggestions ALTER COLUMN invoice_id DROP NOT NULL;
ALTER TABLE money_suggestions ADD COLUMN order_id uuid REFERENCES storefront_orders(id);
ALTER TABLE money_suggestions ADD COLUMN order_payment_id uuid REFERENCES storefront_order_payments(id); -- the match it became
ALTER TABLE money_suggestions ADD CONSTRAINT money_suggestions_target CHECK ((invoice_id IS NULL) <> (order_id IS NULL));
ALTER TABLE money_suggestions ADD CONSTRAINT money_suggestions_entry_order UNIQUE (entry_id, order_id);
GRANT UPDATE (order_payment_id) ON money_suggestions TO taskiem_app;

-- Tenants with matches to copy to money_matches: invoices' and orders'.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_unmirrored_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.tenant_id FROM customer_invoice_payments p WHERE p.mirrored_at IS NULL
  UNION
  SELECT o.tenant_id FROM storefront_order_payments o WHERE o.mirrored_at IS NULL
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_unmirrored_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT p.tenant_id FROM customer_invoice_payments p WHERE p.mirrored_at IS NULL
$$;
-- +goose StatementEnd
REVOKE UPDATE (order_payment_id) ON money_suggestions FROM taskiem_app;
DELETE FROM money_suggestions WHERE order_id IS NOT NULL;
ALTER TABLE money_suggestions DROP CONSTRAINT money_suggestions_entry_order;
ALTER TABLE money_suggestions DROP CONSTRAINT money_suggestions_target;
ALTER TABLE money_suggestions DROP COLUMN order_payment_id;
ALTER TABLE money_suggestions DROP COLUMN order_id;
ALTER TABLE money_suggestions ALTER COLUMN invoice_id SET NOT NULL;
DROP INDEX storefront_orders_payment_ref;
REVOKE SELECT ON storefront_order_payments FROM taskiem_dispatch;
DROP TABLE storefront_order_payments;
