-- A customer's payment link (decision 0060; docs/spec/no-sheet-needed.md
-- §6.4, R5): one page per customer, /c/{token} on the form host, listing
-- what they owe across their open invoices (credit lines and sent ones)
-- and taking a payment towards the total, part or whole. "send Ada her
-- balance" sends it, so a customer pays one amount, not one invoice at a
-- time.
--
-- The link is kept with the customer (invoice_customer_links, one per
-- customer_id, as customer_accounts keeps a permanent account number), its
-- token sealed under the customer's subject, as an invoice's is. Its
-- account numbers and card links are the invoices' (customer_invoice_
-- accounts, with a third owner beside an invoice and an order), so
-- issuing, the limits, the payment id written before the rail is asked,
-- the lookups and expiry are the invoices' code, and the page's requests
-- are the invoices' queue. A payment into one is the customer's money,
-- counted exactly towards their open invoices oldest first, the rest
-- their credit (engine/matching CountCustomer, money_entry_customers how
-- link): no invoice is folded into another or voided.
-- +goose Up
CREATE TABLE invoice_customer_links (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  customer_id      text NOT NULL,          -- as customer_invoices.customer_id
  page_token       jsonb NOT NULL,         -- sealed under the customer's subject
  page_token_hash  bytea NOT NULL UNIQUE,
  created_by       text NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, customer_id)
);
ALTER TABLE invoice_customer_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_customer_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_customer_links TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON invoice_customer_links TO taskiem_app;
CREATE POLICY dispatch ON invoice_customer_links FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, customer_id, page_token_hash) ON invoice_customer_links TO taskiem_dispatch;

ALTER TABLE customer_invoice_accounts ADD COLUMN customer_link_id uuid REFERENCES invoice_customer_links(id),
  DROP CONSTRAINT customer_invoice_accounts_one_owner,
  ADD CONSTRAINT customer_invoice_accounts_one_owner CHECK (num_nonnulls(invoice_id, order_id, customer_link_id) = 1),
  ADD CONSTRAINT customer_invoice_accounts_link_seq UNIQUE (customer_link_id, kind, seq);
CREATE INDEX customer_invoice_accounts_link ON customer_invoice_accounts (customer_link_id) WHERE customer_link_id IS NOT NULL;

ALTER TABLE customer_invoice_requests ADD COLUMN customer_link_id uuid REFERENCES invoice_customer_links(id),
  DROP CONSTRAINT customer_invoice_requests_one_owner,
  ADD CONSTRAINT customer_invoice_requests_one_owner CHECK (num_nonnulls(invoice_id, order_id, customer_link_id) = 1);

-- Money paid through a customer's link is theirs, as money into their own
-- account number is.
ALTER TABLE money_entry_customers DROP CONSTRAINT money_entry_customers_how_check;
ALTER TABLE money_entry_customers ADD CONSTRAINT money_entry_customers_how_check CHECK (how IN ('account', 'person', 'link'));

-- The claim names the link too (a new result column: dropped and made
-- again).
DROP FUNCTION taskiem_invoice_requests_claim(int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_requests_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, invoice_id uuid, order_id uuid, customer_link_id uuid, kind text, amount_kobo bigint, channel text, reply_to jsonb)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE customer_invoice_requests r SET claimed_until = now() + interval '1 minute'
   WHERE r.id IN (SELECT x.id FROM customer_invoice_requests x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING r.id, r.tenant_id, r.invoice_id, r.order_id, r.customer_link_id, r.kind, r.amount_kobo, r.channel, r.reply_to
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_invoice_requests_claim(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_invoice_requests_claim(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_invoice_requests_claim(int) TO taskiem_app;

-- What a customer's page shows: the business, and the customer's open
-- invoices oldest first (as their money pays them), numbers, lines and
-- what is due on each. Nothing personal: the customer's name stays
-- sealed. No rows for a token that is unknown, an inactive business's, or
-- a customer who owes nothing (the page says so).
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_customer_link(p_token_hash bytea)
RETURNS TABLE (tenant_id uuid, number text, status text, lines jsonb, total_kobo bigint, paid_kobo bigint, due_on date)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT i.tenant_id, i.number, i.status, i.lines, i.total_kobo, i.paid_kobo, i.due_on
    FROM invoice_customer_links l
    JOIN tenants t ON t.id = l.tenant_id AND t.status = 'active'
    JOIN customer_invoices i ON i.tenant_id = l.tenant_id AND i.customer_id = l.customer_id
   WHERE l.page_token_hash = p_token_hash AND i.status IN ('recorded', 'sent', 'part_paid', 'overdue')
   ORDER BY COALESCE(i.due_on, (COALESCE(i.sent_at, i.recorded_at) AT TIME ZONE 'Africa/Lagos')::date), COALESCE(i.sent_at, i.recorded_at), i.number
   LIMIT 50
$$;
-- +goose StatementEnd

-- The page asks for an account number or a card link towards what the
-- customer owes: the whole (0), or a part of at least ₦1,000 (or what is
-- left, when less). Refused (NULL) when nothing is owed, for an amount
-- above it, or past the limits (five an hour and twenty in all per link
-- and kind). A request already waiting is not repeated.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_customer_link_request(p_token_hash bytea, p_kind text, p_amount_kobo bigint)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  lid uuid;
  tid uuid;
  cid text;
  due bigint;
  rid uuid;
BEGIN
  IF p_kind NOT IN ('transfer', 'link') OR p_amount_kobo < 0 THEN
    RETURN NULL;
  END IF;
  SELECT l.id, l.tenant_id, l.customer_id INTO lid, tid, cid FROM invoice_customer_links l JOIN tenants t ON t.id = l.tenant_id AND t.status = 'active'
   WHERE l.page_token_hash = p_token_hash;
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  -- One at a time for a link.
  PERFORM pg_advisory_xact_lock(hashtext('customer-link:' || lid::text));
  SELECT COALESCE(sum(i.total_kobo - i.paid_kobo), 0) INTO due FROM customer_invoices i
   WHERE i.tenant_id = tid AND i.customer_id = cid AND i.status IN ('recorded', 'sent', 'part_paid', 'overdue');
  IF due <= 0 OR p_amount_kobo > due OR p_amount_kobo > 0 AND p_amount_kobo < LEAST(100000, due) THEN
    RETURN NULL;
  END IF;
  SELECT r.id INTO rid FROM customer_invoice_requests r WHERE r.customer_link_id = lid AND r.kind = p_kind;
  IF rid IS NOT NULL THEN
    RETURN rid;
  END IF;
  IF (SELECT count(*) FROM customer_invoice_accounts a WHERE a.customer_link_id = lid AND a.kind = p_kind AND a.created_at > now() - interval '1 hour') >= 5
     OR (SELECT count(*) FROM customer_invoice_accounts a WHERE a.customer_link_id = lid AND a.kind = p_kind) >= 20 THEN
    RETURN NULL;
  END IF;
  rid := gen_random_uuid();
  INSERT INTO customer_invoice_requests (id, tenant_id, customer_link_id, kind, amount_kobo) VALUES (rid, tid, lid, p_kind, p_amount_kobo);
  RETURN rid;
END
$$;
-- +goose StatementEnd

-- What the page shows of the link's latest account number and card link,
-- as an invoice's page shows its own; a customer link always takes part
-- payments.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_customer_link_payment(p_token_hash bytea)
RETURNS TABLE (kind text, status text, amount_kobo bigint, account_number text, bank_name text, account_name text, payment_url text,
               expires_at timestamptz, on_late text, on_wrong_amount text, waiting boolean, issued_last_hour int, issued int, part_payments boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH l AS (
    SELECT l.id FROM invoice_customer_links l JOIN tenants t ON t.id = l.tenant_id AND t.status = 'active' WHERE l.page_token_hash = p_token_hash
  ), k(kind) AS (VALUES ('transfer'), ('link'))
  SELECT k.kind, a.status, a.amount_kobo, a.account_number, a.bank_name, a.account_name, a.payment_url, a.expires_at, a.on_late, a.on_wrong_amount,
         EXISTS (SELECT 1 FROM customer_invoice_requests r WHERE r.customer_link_id = l.id AND r.kind = k.kind),
         (SELECT count(*)::int FROM customer_invoice_accounts c WHERE c.customer_link_id = l.id AND c.kind = k.kind AND c.created_at > now() - interval '1 hour'),
         (SELECT count(*)::int FROM customer_invoice_accounts c WHERE c.customer_link_id = l.id AND c.kind = k.kind),
         true
    FROM k CROSS JOIN l
    LEFT JOIN LATERAL (SELECT * FROM customer_invoice_accounts a WHERE a.customer_link_id = l.id AND a.kind = k.kind ORDER BY a.seq DESC LIMIT 1) a ON true
$$;
-- +goose StatementEnd

-- The customer's own account number, when the business registered one:
-- the page shows it first, as an invoice's page does.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_customer_link_own_account(p_token_hash bytea)
RETURNS TABLE (account_number text, bank_name text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.account_number, COALESCE(a.bank_name, '')
    FROM invoice_customer_links l
    JOIN tenants t ON t.id = l.tenant_id AND t.status = 'active'
    JOIN customer_accounts a ON a.tenant_id = l.tenant_id AND a.customer_id = l.customer_id
   WHERE l.page_token_hash = p_token_hash
   ORDER BY a.created_at DESC, a.id
   LIMIT 1
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_customer_link(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_customer_link_request(bytea, text, bigint) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_customer_link_payment(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_customer_link_own_account(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_customer_link(bytea), taskiem_public_customer_link_request(bytea, text, bigint),
  taskiem_public_customer_link_payment(bytea), taskiem_public_customer_link_own_account(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_customer_link(bytea), taskiem_public_customer_link_request(bytea, text, bigint),
  taskiem_public_customer_link_payment(bytea), taskiem_public_customer_link_own_account(bytea) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_customer_link_own_account(bytea);
DROP FUNCTION taskiem_public_customer_link_payment(bytea);
DROP FUNCTION taskiem_public_customer_link_request(bytea, text, bigint);
DROP FUNCTION taskiem_public_customer_link(bytea);
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
DELETE FROM money_entry_customers WHERE how = 'link';
ALTER TABLE money_entry_customers DROP CONSTRAINT money_entry_customers_how_check;
ALTER TABLE money_entry_customers ADD CONSTRAINT money_entry_customers_how_check CHECK (how IN ('account', 'person'));
DELETE FROM customer_invoice_requests WHERE customer_link_id IS NOT NULL;
ALTER TABLE customer_invoice_requests DROP CONSTRAINT customer_invoice_requests_one_owner, DROP COLUMN customer_link_id,
  ADD CONSTRAINT customer_invoice_requests_one_owner CHECK ((invoice_id IS NULL) <> (order_id IS NULL));
DELETE FROM customer_invoice_accounts WHERE customer_link_id IS NOT NULL;
DROP INDEX customer_invoice_accounts_link;
ALTER TABLE customer_invoice_accounts DROP CONSTRAINT customer_invoice_accounts_link_seq, DROP CONSTRAINT customer_invoice_accounts_one_owner,
  DROP COLUMN customer_link_id, ADD CONSTRAINT customer_invoice_accounts_one_owner CHECK ((invoice_id IS NULL) <> (order_id IS NULL));
DROP TABLE invoice_customer_links;
