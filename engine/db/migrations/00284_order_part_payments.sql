-- Part payments for storefront submissions (docs/spec/fronts.md §5.4,
-- F5): a deposit then the balance, an instalment, a late fee. An order
-- keeps what it costs (its total, plus extra_kobo for a late fee) and what
-- was paid towards it (paid_kobo, kept from its payment rows as an
-- invoice's is). The first payment works as before, its parts adding up to
-- the deposit or the total; a later step asks for more on an order already
-- paid: pay_to_kobo is what paid_kobo is to reach, pay_step_kobo what the
-- step asked, and pay_seq numbers the steps, so each has its own checkout
-- reference.

-- +goose Up
ALTER TABLE storefront_orders
  ADD COLUMN extra_kobo  bigint NOT NULL DEFAULT 0 CHECK (extra_kobo >= 0),
  ADD COLUMN due_kobo    bigint GENERATED ALWAYS AS (total_kobo + extra_kobo) STORED,
  ADD COLUMN paid_kobo   bigint NOT NULL DEFAULT 0 CHECK (paid_kobo >= 0),
  ADD COLUMN pay_to_kobo bigint CHECK (pay_to_kobo > 0),
  ADD COLUMN pay_step_kobo bigint CHECK (pay_step_kobo > 0),
  ADD COLUMN pay_seq     int NOT NULL DEFAULT 0 CHECK (pay_seq >= 0);

-- What was paid: the payment rows not undone, or, for an order paid before
-- payments had rows, what it asked.
UPDATE storefront_orders o SET paid_kobo = COALESCE((SELECT sum(p.amount_kobo) FROM storefront_order_payments p
   WHERE p.order_id = o.id AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)), 0);
UPDATE storefront_orders SET paid_kobo = COALESCE(deposit_kobo, total_kobo) WHERE paid_at IS NOT NULL AND paid_kobo = 0;

-- Each payment row, and each undo, keeps the order's paid_kobo.
-- +goose StatementBegin
CREATE FUNCTION taskiem_order_paid_kobo() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  UPDATE storefront_orders o SET paid_kobo = COALESCE((SELECT sum(p.amount_kobo) FROM storefront_order_payments p
     WHERE p.order_id = o.id AND p.undoes IS NULL AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)), 0),
     updated_at = now()
   WHERE o.id = NEW.order_id;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER order_paid_kobo AFTER INSERT ON storefront_order_payments
  FOR EACH ROW EXECUTE FUNCTION taskiem_order_paid_kobo();
-- The status page's transfer request (owned by taskiem_dispatch) takes a
-- later step too.
GRANT SELECT (pay_to_kobo) ON storefront_orders TO taskiem_dispatch;

-- What the customer's status page shows of the money: what it costs, what
-- is paid, and what is asked now (a deposit, the balance, an instalment);
-- nothing personal.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_order_money(p_reference text, p_status_hash bytea)
RETURNS TABLE (due_kobo bigint, paid_kobo bigint, asked_kobo bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.due_kobo, o.paid_kobo,
         CASE WHEN o.pay_to_kobo IS NOT NULL THEN GREATEST(o.pay_to_kobo - o.paid_kobo, 0)
              WHEN o.status = 'awaiting_payment' THEN GREATEST(COALESCE(o.deposit_kobo, o.total_kobo) - o.paid_kobo, 0)
              ELSE 0 END
    FROM storefront_orders o
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION taskiem_public_order_money(text, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_order_money(text, bytea) TO taskiem_public;


-- A later step is paid by transfer from the status page too.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_order_request(p_reference text, p_status_hash bytea)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  oid uuid;
  tid uuid;
  rid uuid;
BEGIN
  SELECT o.id, o.tenant_id INTO oid, tid FROM storefront_orders o JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active'
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash
     AND (o.status = 'awaiting_payment' AND (o.expires_at IS NULL OR o.expires_at > now())
       OR o.pay_to_kobo IS NOT NULL AND o.status NOT IN ('refunded', 'refund_due', 'cancelled', 'declined', 'expired')) FOR UPDATE OF o;
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  SELECT r.id INTO rid FROM customer_invoice_requests r WHERE r.order_id = oid AND r.kind = 'transfer';
  IF rid IS NOT NULL THEN
    RETURN rid;
  END IF;
  IF (SELECT count(*) FROM customer_invoice_accounts a WHERE a.order_id = oid AND a.kind = 'transfer' AND a.created_at > now() - interval '1 hour') >= 5
     OR (SELECT count(*) FROM customer_invoice_accounts a WHERE a.order_id = oid AND a.kind = 'transfer') >= 20 THEN
    RETURN NULL;
  END IF;
  rid := gen_random_uuid();
  INSERT INTO customer_invoice_requests (id, tenant_id, order_id, kind) VALUES (rid, tid, oid, 'transfer');
  RETURN rid;
END
$$;
-- +goose StatementEnd

-- The payment check looks at a later step paid by card too.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT o.tenant_id FROM storefront_orders o WHERE o.status = 'awaiting_payment' OR o.pay_to_kobo IS NOT NULL AND o.payment_url IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT o.tenant_id FROM storefront_orders o WHERE o.status = 'awaiting_payment'
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_order_request(p_reference text, p_status_hash bytea)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  oid uuid;
  tid uuid;
  rid uuid;
BEGIN
  SELECT o.id, o.tenant_id INTO oid, tid FROM storefront_orders o JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active'
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash
     AND o.status = 'awaiting_payment'
     AND (o.expires_at IS NULL OR o.expires_at > now()) FOR UPDATE OF o;
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  SELECT r.id INTO rid FROM customer_invoice_requests r WHERE r.order_id = oid AND r.kind = 'transfer';
  IF rid IS NOT NULL THEN
    RETURN rid;
  END IF;
  IF (SELECT count(*) FROM customer_invoice_accounts a WHERE a.order_id = oid AND a.kind = 'transfer' AND a.created_at > now() - interval '1 hour') >= 5
     OR (SELECT count(*) FROM customer_invoice_accounts a WHERE a.order_id = oid AND a.kind = 'transfer') >= 20 THEN
    RETURN NULL;
  END IF;
  rid := gen_random_uuid();
  INSERT INTO customer_invoice_requests (id, tenant_id, order_id, kind) VALUES (rid, tid, oid, 'transfer');
  RETURN rid;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_public_order_money(text, bytea);
DROP TRIGGER order_paid_kobo ON storefront_order_payments;
DROP FUNCTION taskiem_order_paid_kobo();
ALTER TABLE storefront_orders DROP COLUMN pay_seq, DROP COLUMN pay_step_kobo, DROP COLUMN pay_to_kobo, DROP COLUMN paid_kobo, DROP COLUMN due_kobo, DROP COLUMN extra_kobo;
