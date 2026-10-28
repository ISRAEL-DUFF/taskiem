-- Pay by bank transfer on the storefront (docs/spec/iswallet-account-
-- numbers.md N3): an order or booking waiting for its payment gets a
-- one-time account number on the business's rail, asked for from its
-- status page, as an invoice's page asks (00198). The accounts are the
-- invoices' table, holding an order or an invoice, so issuing, the limits,
-- the payment id recorded before the rail is asked, the lookups and expiry
-- are the invoices' code; and the page's requests are the invoices'
-- queue. A payment into an order's account is counted towards the order
-- (engine/storefront), never an invoice.

-- +goose Up
ALTER TABLE customer_invoice_accounts ALTER COLUMN invoice_id DROP NOT NULL,
  ADD COLUMN order_id uuid REFERENCES storefront_orders(id),
  ADD CONSTRAINT customer_invoice_accounts_one_owner CHECK ((invoice_id IS NULL) <> (order_id IS NULL)),
  ADD CONSTRAINT customer_invoice_accounts_order_seq UNIQUE (order_id, kind, seq);
CREATE INDEX customer_invoice_accounts_order ON customer_invoice_accounts (order_id) WHERE order_id IS NOT NULL;

ALTER TABLE customer_invoice_requests ALTER COLUMN invoice_id DROP NOT NULL,
  ADD COLUMN order_id uuid REFERENCES storefront_orders(id),
  ADD CONSTRAINT customer_invoice_requests_one_owner CHECK ((invoice_id IS NULL) <> (order_id IS NULL));

-- The claim names the order too (a new result column: dropped and made
-- again).
DROP FUNCTION taskiem_invoice_requests_claim(int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_requests_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, invoice_id uuid, order_id uuid, kind text, amount_kobo bigint, channel text, reply_to jsonb)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE customer_invoice_requests r SET claimed_until = now() + interval '1 minute'
   WHERE r.id IN (SELECT x.id FROM customer_invoice_requests x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING r.id, r.tenant_id, r.invoice_id, r.order_id, r.kind, r.amount_kobo, r.channel, r.reply_to
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_invoice_requests_claim(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_invoice_requests_claim(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_invoice_requests_claim(int) TO taskiem_app;

-- An order's status page asks for an account number: refused (NULL) for
-- an order not waiting for its payment, a business not active, or past
-- the limits (five an hour and twenty in all per order, as an invoice's).
-- A request already waiting is not repeated. Whether the business's rail
-- can issue one is the api role's to find out.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_order_request(p_reference text, p_status_hash bytea)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  oid uuid;
  tid uuid;
  rid uuid;
BEGIN
  -- The columns the owner (taskiem_dispatch) may read, locked against a
  -- second request racing this one.
  SELECT o.id, o.tenant_id INTO oid, tid FROM storefront_orders o JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active'
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash AND o.status = 'awaiting_payment'
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

-- What an order's status page shows of paying by transfer: the latest
-- account number (the business's collection account, never the
-- customer's), whether a request waits, the limits, and whether the form
-- takes card payments through Paystack too (its connection names no other
-- rail), so the page knows a card page is coming; and until when the order
-- is held for its payment.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_order_payment(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, amount_kobo bigint, account_number text, bank_name text, account_name text, expires_at timestamptz, on_late text,
               on_wrong_amount text, waiting boolean, issued_last_hour int, issued int, card boolean, held_until timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH ord AS (
    SELECT o.id, o.expires_at, COALESCE(a.options->>'connection', '') AS connection FROM storefront_orders o
      JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active'
      LEFT JOIN storefront_actions a ON a.storefront_id = o.storefront_id AND a.kind = o.kind
     WHERE o.reference = p_reference AND o.status_hash = p_status_hash
  )
  SELECT a.status, a.amount_kobo, a.account_number, a.bank_name, a.account_name, a.expires_at, a.on_late, a.on_wrong_amount,
         EXISTS (SELECT 1 FROM customer_invoice_requests r WHERE r.order_id = ord.id AND r.kind = 'transfer'),
         (SELECT count(*)::int FROM customer_invoice_accounts c WHERE c.order_id = ord.id AND c.kind = 'transfer' AND c.created_at > now() - interval '1 hour'),
         (SELECT count(*)::int FROM customer_invoice_accounts c WHERE c.order_id = ord.id AND c.kind = 'transfer'),
         ord.connection NOT LIKE '%/%' OR ord.connection LIKE 'paystack/%', ord.expires_at
    FROM ord
    LEFT JOIN LATERAL (SELECT * FROM customer_invoice_accounts a WHERE a.order_id = ord.id AND a.kind = 'transfer' ORDER BY a.seq DESC LIMIT 1) a ON true
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_order_request(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_order_payment(text, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_order_request(text, bytea), taskiem_public_order_payment(text, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_order_request(text, bytea), taskiem_public_order_payment(text, bytea) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_order_payment(text, bytea);
DROP FUNCTION taskiem_public_order_request(text, bytea);
DROP FUNCTION taskiem_invoice_requests_claim(int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_requests_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, invoice_id uuid, kind text, amount_kobo bigint, channel text, reply_to jsonb)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE customer_invoice_requests r SET claimed_until = now() + interval '1 minute'
   WHERE r.id IN (SELECT x.id FROM customer_invoice_requests x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING r.id, r.tenant_id, r.invoice_id, r.kind, r.amount_kobo, r.channel, r.reply_to
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_invoice_requests_claim(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_invoice_requests_claim(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_invoice_requests_claim(int) TO taskiem_app;
DELETE FROM customer_invoice_requests WHERE order_id IS NOT NULL;
ALTER TABLE customer_invoice_requests DROP CONSTRAINT customer_invoice_requests_one_owner, DROP COLUMN order_id, ALTER COLUMN invoice_id SET NOT NULL;
DELETE FROM customer_invoice_accounts WHERE order_id IS NOT NULL;
DROP INDEX customer_invoice_accounts_order;
ALTER TABLE customer_invoice_accounts DROP CONSTRAINT customer_invoice_accounts_order_seq, DROP CONSTRAINT customer_invoice_accounts_one_owner,
  DROP COLUMN order_id, ALTER COLUMN invoice_id SET NOT NULL;
