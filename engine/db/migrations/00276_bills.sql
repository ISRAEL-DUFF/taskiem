-- Business links, L1: bills (decision 0061; docs/spec/business-links.md
-- §6). A bill is the buyer's copy of a linked supplier's invoice: the
-- invoice's number, lines, total, due date and what the seller's match has
-- counted paid are the seller's (mirrored, never edited here); disputed and
-- declared_paid_kobo are the buyer's own.
--
-- An invoice is mirrored when its customer key is link:<buyer> (the seller
-- chose the linked business as the customer), once it is sent. The seller's
-- own transaction only notes the invoice in link_mirrors_due (a trigger on
-- customer_invoices, so every path that changes an invoice is covered: send,
-- amend, void, a payment counted or undone, overdue); a worker then
-- mirrors it in a transaction of its own, entering the buyer's side through
-- taskiem_link_enter. A mirror that cannot be written yet stays due and is
-- tried again, and the seller's invoice is never refused for it.
-- +goose Up
CREATE TABLE bills (
  id                 uuid PRIMARY KEY,
  tenant_id          uuid NOT NULL REFERENCES tenants(id),          -- the buyer
  link_id            uuid NOT NULL REFERENCES business_links(id),
  supplier_tenant    uuid NOT NULL REFERENCES tenants(id),          -- the seller
  supplier_name      text NOT NULL CHECK (length(supplier_name) BETWEEN 1 AND 200),
  source_id          uuid NOT NULL,                                  -- the seller's invoice
  reference          text NOT NULL CHECK (length(reference) BETWEEN 1 AND 40),
  lines              jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array'),
  amount_kobo        bigint NOT NULL CHECK (amount_kobo > 0),
  paid_kobo          bigint NOT NULL DEFAULT 0 CHECK (paid_kobo >= 0),
  due_on             date,
  voided             boolean NOT NULL DEFAULT false,
  allow_part_payments boolean NOT NULL DEFAULT true,
  -- The way to pay, the seller's: the account number it issued this buyer
  -- (§6.3), and the invoice's own page, which takes a card or a transfer.
  pay_account_number text CHECK (pay_account_number ~ '^[0-9A-Za-z-]{4,40}$'),
  pay_bank_name      text CHECK (length(pay_bank_name) <= 120),
  pay_link           text CHECK (length(pay_link) <= 500),
  -- The buyer's own: a query on the bill, and what it says it paid
  -- outside Taskiem (never compared with paid_kobo by the reconciler).
  disputed           boolean NOT NULL DEFAULT false,
  declared_paid_kobo bigint NOT NULL DEFAULT 0 CHECK (declared_paid_kobo >= 0),
  -- The buyer's own payments from Taskiem: under way (a payout created and
  -- not yet settled or refused), and sent (the rail confirmed). What the
  -- seller counts is paid_kobo, when its match says so.
  paying_kobo        bigint NOT NULL DEFAULT 0 CHECK (paying_kobo >= 0),
  sent_kobo          bigint NOT NULL DEFAULT 0 CHECK (sent_kobo >= 0),
  status             text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'paying', 'part_paid', 'paid', 'disputed', 'void')),
  mirrored_at        timestamptz NOT NULL DEFAULT now(),
  created_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, link_id, source_id)
);
CREATE INDEX bills_open ON bills (tenant_id, due_on) WHERE status NOT IN ('paid', 'void');
ALTER TABLE bills ENABLE ROW LEVEL SECURITY;
ALTER TABLE bills FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bills TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON bills TO taskiem_app;

-- A bill's status, from the seller's figures and the buyer's own flags.
-- +goose StatementBegin
CREATE FUNCTION taskiem_bill_status() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.status := CASE
    WHEN NEW.voided THEN 'void'
    WHEN NEW.paid_kobo >= NEW.amount_kobo THEN 'paid'
    WHEN NEW.paying_kobo > 0 THEN 'paying'
    WHEN NEW.disputed THEN 'disputed'
    WHEN NEW.paid_kobo > 0 THEN 'part_paid'
    ELSE 'open' END;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER bill_status BEFORE INSERT OR UPDATE ON bills FOR EACH ROW EXECUTE FUNCTION taskiem_bill_status();

-- Paying a bill from Taskiem (§6.2): each payout the buyer starts, through
-- its own payments connection under its approval policy (the managed
-- workflow in bill_pay_settings). token is what the payout's narration
-- carries, the bill's reference and a short code (INV-0042/7QX4), which
-- the seller's matching reads to count the money towards that invoice.
CREATE TABLE bill_payments (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  bill_id         uuid NOT NULL REFERENCES bills(id),
  amount_kobo     bigint NOT NULL CHECK (amount_kobo > 0),
  token           text NOT NULL CHECK (length(token) BETWEEN 1 AND 60),
  connection      text NOT NULL,
  bank_code       text NOT NULL,
  account_number  text NOT NULL,
  account_name    text,
  run_id          uuid,
  status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed', 'rejected')),
  created_by      text NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  finished_at     timestamptz,
  UNIQUE (tenant_id, token)
);
CREATE INDEX bill_payments_bill ON bill_payments (tenant_id, bill_id);
ALTER TABLE bill_payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE bill_payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bill_payments TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON bill_payments TO taskiem_app;

-- How a business pays its bills: the managed workflow made from the
-- pay-linked-bill template, and what it was made with.
CREATE TABLE bill_pay_settings (
  tenant_id        uuid PRIMARY KEY REFERENCES tenants(id),
  workflow_id      uuid NOT NULL,
  connection       text NOT NULL,
  funding_account  text NOT NULL DEFAULT '',
  approver_role    text NOT NULL,
  updated_by       text NOT NULL,
  updated_at       timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE bill_pay_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE bill_pay_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bill_pay_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON bill_pay_settings TO taskiem_app;

-- The seller's invoices waiting to be mirrored: the seller's own rows.
CREATE TABLE link_mirrors_due (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  invoice_id  uuid NOT NULL,
  due_at      timestamptz NOT NULL DEFAULT now(),
  tries       int NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, invoice_id)
);
ALTER TABLE link_mirrors_due ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_mirrors_due FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_mirrors_due TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON link_mirrors_due TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON link_mirrors_due TO taskiem_app;
GRANT SELECT, INSERT, UPDATE ON link_mirrors_due TO taskiem_dispatch;

-- Notes a sent invoice to a linked business as due to be mirrored, on any
-- change. As its owner, so a change the dispatch role makes (the overdue
-- check) is noted too.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_mirror_note() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO link_mirrors_due (tenant_id, invoice_id) VALUES (NEW.tenant_id, NEW.id)
  ON CONFLICT (tenant_id, invoice_id) DO UPDATE SET due_at = now(), tries = 0;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_mirror_note() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_mirror_note() FROM PUBLIC;
CREATE TRIGGER customer_invoice_link_mirror AFTER INSERT OR UPDATE ON customer_invoices
  FOR EACH ROW WHEN (NEW.customer_key LIKE 'link:%' AND NEW.sent_at IS NOT NULL)
  EXECUTE FUNCTION taskiem_link_mirror_note();

-- The worker's claim: invoices due to be mirrored, across businesses,
-- oldest first, each held for a minute; tenant and invoice ids only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_mirrors_claim(p_limit int)
RETURNS TABLE (tenant_id uuid, invoice_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE link_mirrors_due d SET due_at = now() + interval '1 minute' * LEAST(60, power(2, d.tries)), tries = d.tries + 1
   WHERE (d.tenant_id, d.invoice_id) IN (SELECT x.tenant_id, x.invoice_id FROM link_mirrors_due x WHERE x.due_at <= now()
                                          ORDER BY x.due_at LIMIT LEAST(GREATEST(p_limit, 1), 100) FOR UPDATE SKIP LOCKED)
  RETURNING d.tenant_id, d.invoice_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_mirrors_claim(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_mirrors_claim(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_mirrors_claim(int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_link_mirrors_claim(int);
DROP TRIGGER customer_invoice_link_mirror ON customer_invoices;
DROP FUNCTION taskiem_link_mirror_note();
DROP TABLE link_mirrors_due;
DROP TABLE bill_pay_settings;
DROP TABLE bill_payments;
DROP TRIGGER bill_status ON bills;
DROP FUNCTION taskiem_bill_status();
DROP TABLE bills;
