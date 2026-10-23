-- Paying an invoice (decision 0045, T4; docs/spec/invoices-and-money-log.md
-- 6; docs/invoices.md): one-time account numbers and payment links issued
-- for an invoice, and the invoice page's requests for them.
--
-- The page is served by the public role, which holds no rail keys: it
-- leaves a request (taskiem_public_invoice_request) that the api role
-- takes within a second, issues with the business's connection, and
-- records here. Every account issued for an invoice stays tied to it, so
-- money paid into an old one still counts. A payment is counted by
-- inserting into customer_invoice_payments (00197), never here.
-- +goose Up
CREATE TABLE customer_invoice_accounts (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  invoice_id        uuid NOT NULL REFERENCES customer_invoices(id),
  seq               int NOT NULL CHECK (seq >= 1),
  kind              text NOT NULL CHECK (kind IN ('transfer', 'link')),
  connector         text NOT NULL,
  connection        text NOT NULL,
  payment_id        text NOT NULL,          -- what the rail's payment events and lookups carry
  amount_kobo       bigint NOT NULL CHECK (amount_kobo > 0),
  status            text NOT NULL DEFAULT 'requested' CHECK (status IN ('requested', 'open', 'failed', 'paid', 'returned')),
  account_number    text,                   -- the business's collection account, not the customer's
  bank_name         text,
  account_name      text,
  payment_url       text,
  expires_at        timestamptz,
  on_late           text,                   -- the rail's facts, for the page's words
  on_wrong_amount   text,
  error             text,
  checked_at        timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (invoice_id, kind, seq),
  UNIQUE (tenant_id, connector, payment_id)
);
CREATE INDEX customer_invoice_accounts_check ON customer_invoice_accounts (checked_at NULLS FIRST) WHERE status IN ('requested', 'open');
ALTER TABLE customer_invoice_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_invoice_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_invoice_accounts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON customer_invoice_accounts TO taskiem_app;
CREATE POLICY dispatch ON customer_invoice_accounts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON customer_invoice_accounts TO taskiem_dispatch;

-- The page's requests: platform data, written by the public role through
-- taskiem_public_invoice_request only, taken by the api role.
CREATE TABLE customer_invoice_requests (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  invoice_id     uuid NOT NULL REFERENCES customer_invoices(id),
  kind           text NOT NULL CHECK (kind IN ('transfer', 'link')),
  amount_kobo    bigint NOT NULL DEFAULT 0 CHECK (amount_kobo >= 0),  -- 0: what is due
  channel        text NOT NULL DEFAULT 'page' CHECK (channel IN ('page', 'whatsapp', 'telegram')),
  reply_to       jsonb,                                   -- a chat request's reply address, sealed
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until  timestamptz
);
CREATE INDEX customer_invoice_requests_open ON customer_invoice_requests (created_at);
ALTER TABLE customer_invoice_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_invoice_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON customer_invoice_requests TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON customer_invoice_requests TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_invoice_requests TO taskiem_dispatch;

-- The page asks for an account number or a payment link for the invoice
-- of this token. Refused (NULL) for an invoice that is not open, an
-- amount the invoice does not allow, or past the limits: five requests an
-- hour and twenty in all per invoice and kind. A request already waiting
-- is not repeated.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_invoice_request(p_token_hash bytea, p_kind text, p_amount_kobo bigint)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  inv customer_invoices%ROWTYPE;
  due bigint;
  rid uuid;
BEGIN
  IF p_kind NOT IN ('transfer', 'link') OR p_amount_kobo < 0 THEN
    RETURN NULL;
  END IF;
  SELECT i.* INTO inv FROM customer_invoices i JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE i.page_token_hash = p_token_hash AND i.status IN ('sent', 'part_paid', 'overdue') FOR UPDATE OF i;
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  due := inv.total_kobo - inv.paid_kobo;
  IF p_amount_kobo > 0 AND p_amount_kobo <> due
     AND (NOT inv.allow_part_payments OR p_amount_kobo > due OR p_amount_kobo < LEAST(100000, due)) THEN
    RETURN NULL;
  END IF;
  SELECT r.id INTO rid FROM customer_invoice_requests r WHERE r.invoice_id = inv.id AND r.kind = p_kind;
  IF rid IS NOT NULL THEN
    RETURN rid;
  END IF;
  IF (SELECT count(*) FROM customer_invoice_accounts a WHERE a.invoice_id = inv.id AND a.kind = p_kind AND a.created_at > now() - interval '1 hour') >= 5
     OR (SELECT count(*) FROM customer_invoice_accounts a WHERE a.invoice_id = inv.id AND a.kind = p_kind) >= 20 THEN
    RETURN NULL;
  END IF;
  rid := gen_random_uuid();
  INSERT INTO customer_invoice_requests (id, tenant_id, invoice_id, kind, amount_kobo) VALUES (rid, inv.tenant_id, inv.id, p_kind, p_amount_kobo);
  RETURN rid;
END
$$;
-- +goose StatementEnd

-- What the page shows of an invoice's latest account number and payment
-- link, and whether a request is waiting: the business's account details,
-- never the customer's.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_invoice_payment(p_token_hash bytea)
RETURNS TABLE (kind text, status text, amount_kobo bigint, account_number text, bank_name text, account_name text, payment_url text,
               expires_at timestamptz, on_late text, on_wrong_amount text, waiting boolean, issued_last_hour int, issued int, part_payments boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH inv AS (
    SELECT i.id, i.allow_part_payments FROM customer_invoices i JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
     WHERE i.page_token_hash = p_token_hash AND i.sent_at IS NOT NULL
  ), k(kind) AS (VALUES ('transfer'), ('link'))
  SELECT k.kind, a.status, a.amount_kobo, a.account_number, a.bank_name, a.account_name, a.payment_url, a.expires_at, a.on_late, a.on_wrong_amount,
         EXISTS (SELECT 1 FROM customer_invoice_requests r, inv WHERE r.invoice_id = inv.id AND r.kind = k.kind),
         (SELECT count(*)::int FROM customer_invoice_accounts c, inv WHERE c.invoice_id = inv.id AND c.kind = k.kind AND c.created_at > now() - interval '1 hour'),
         (SELECT count(*)::int FROM customer_invoice_accounts c, inv WHERE c.invoice_id = inv.id AND c.kind = k.kind),
         inv.allow_part_payments
    FROM k CROSS JOIN inv
    LEFT JOIN LATERAL (SELECT * FROM customer_invoice_accounts a WHERE a.invoice_id = inv.id AND a.kind = k.kind ORDER BY a.seq DESC LIMIT 1) a ON true
$$;
-- +goose StatementEnd

-- The api role's side of the requests.
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
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_requests_done(p_id uuid)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM customer_invoice_requests WHERE id = p_id
$$;
-- +goose StatementEnd
-- A request a member made (chat), as the api role, for its own tenant.
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_request_put(p_tenant uuid, p_invoice uuid, p_kind text, p_amount_kobo bigint, p_channel text, p_reply_to jsonb)
RETURNS uuid
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO customer_invoice_requests (id, tenant_id, invoice_id, kind, amount_kobo, channel, reply_to)
  SELECT gen_random_uuid(), i.tenant_id, i.id, p_kind, p_amount_kobo, p_channel, p_reply_to FROM customer_invoices i
   WHERE i.id = p_invoice AND i.tenant_id = p_tenant AND p_tenant = ANY (taskiem_tenant_scope())
  RETURNING id
$$;
-- +goose StatementEnd
-- Tenants with accounts or links still to watch, for the payment checker.
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_watch_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT a.tenant_id FROM customer_invoice_accounts a WHERE a.status IN ('requested', 'open')
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_public_invoice_request(bytea, text, bigint) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_invoice_payment(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_invoice_requests_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_invoice_requests_done(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_invoice_request_put(uuid, uuid, text, bigint, text, jsonb) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_invoice_watch_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_invoice_request(bytea, text, bigint), taskiem_public_invoice_payment(bytea), taskiem_invoice_requests_claim(int),
  taskiem_invoice_requests_done(uuid), taskiem_invoice_request_put(uuid, uuid, text, bigint, text, jsonb), taskiem_invoice_watch_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_invoice_request(bytea, text, bigint), taskiem_public_invoice_payment(bytea) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_invoice_requests_claim(int), taskiem_invoice_requests_done(uuid), taskiem_invoice_request_put(uuid, uuid, text, bigint, text, jsonb),
  taskiem_invoice_watch_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_invoice_watch_tenants();
DROP FUNCTION taskiem_invoice_request_put(uuid, uuid, text, bigint, text, jsonb);
DROP FUNCTION taskiem_invoice_requests_done(uuid);
DROP FUNCTION taskiem_invoice_requests_claim(int);
DROP FUNCTION taskiem_public_invoice_payment(bytea);
DROP FUNCTION taskiem_public_invoice_request(bytea, text, bigint);
DROP TABLE customer_invoice_requests;
DROP TABLE customer_invoice_accounts;
