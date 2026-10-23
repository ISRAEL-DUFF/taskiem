-- Invoices a business sends its customers (decision 0045; docs/spec/
-- invoices-and-money-log.md 5, T3; docs/invoices.md). Not Taskiem's own
-- billing invoices, which are the table invoices (00092).
--
-- The rule this schema keeps: an invoice is part-paid or paid only from
-- its payments. Status and paid_kobo are computed by a trigger from
-- sent_at, voided_at, due_on and the payments; nobody writes them. A
-- payment is a row of customer_invoice_payments, inserted and never
-- changed: undoing one inserts a row naming it.
-- +goose Up
CREATE TABLE customer_invoice_settings (
  tenant_id            uuid PRIMARY KEY REFERENCES tenants(id),
  prefix               text NOT NULL DEFAULT 'INV-' CHECK (prefix ~ '^[A-Za-z0-9/_-]{0,12}$'),
  next_number          int NOT NULL DEFAULT 1 CHECK (next_number >= 1),
  connection           text NOT NULL DEFAULT 'main',
  fee_paid_by          text NOT NULL DEFAULT 'business' CHECK (fee_paid_by IN ('business', 'customer')),
  allow_part_payments  boolean NOT NULL DEFAULT false,
  updated_at           timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE customer_invoice_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_invoice_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_invoice_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON customer_invoice_settings TO taskiem_app;

CREATE TABLE customer_invoices (
  id                   uuid PRIMARY KEY,
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  number               text NOT NULL,
  status               text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent', 'part_paid', 'paid', 'overdue', 'void')),
  customer             jsonb NOT NULL,         -- sealed: {name, phone, email}
  subject_id           text NOT NULL,          -- the customer's personal-data subject
  lines                jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array'),
  total_kobo           bigint NOT NULL CHECK (total_kobo > 0),
  currency             text NOT NULL DEFAULT 'NGN' CHECK (currency = 'NGN'),
  due_on               date,
  note                 text CHECK (length(note) <= 500),
  connection           text NOT NULL,
  fee_paid_by          text NOT NULL CHECK (fee_paid_by IN ('business', 'customer')),
  allow_part_payments  boolean NOT NULL,
  paid_kobo            bigint NOT NULL DEFAULT 0,
  paid_at              timestamptz,
  page_token_hash      bytea UNIQUE CHECK (length(page_token_hash) = 32),
  page_token           jsonb,                  -- sealed under the customer's subject, for sending the link again
  sent_at              timestamptz,
  voided_at            timestamptz,
  void_reason          text CHECK (length(void_reason) <= 300),
  created_by           text NOT NULL,
  created_key          text,                   -- a workflow step's key: a retry finds the invoice it made
  run_id               uuid,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, number),
  UNIQUE (tenant_id, created_key),
  CHECK (voided_at IS NULL OR void_reason IS NOT NULL)
);
CREATE INDEX customer_invoices_list ON customer_invoices (tenant_id, created_at DESC);
CREATE INDEX customer_invoices_due ON customer_invoices (due_on) WHERE status IN ('sent', 'part_paid');
ALTER TABLE customer_invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_invoices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_invoices TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON customer_invoices TO taskiem_app;
-- Status, paid_kobo and paid_at are never granted: the trigger sets them.
GRANT UPDATE (customer, subject_id, lines, total_kobo, due_on, note, connection, fee_paid_by, allow_part_payments,
  page_token_hash, page_token, sent_at, voided_at, void_reason, run_id, updated_at) ON customer_invoices TO taskiem_app;

CREATE TABLE customer_invoice_payments (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  invoice_id   uuid NOT NULL REFERENCES customer_invoices(id),
  amount_kobo  bigint NOT NULL CHECK (amount_kobo > 0),
  payment_id   text NOT NULL,                  -- the rail's, or the log entry's
  connection   text,
  how          text NOT NULL CHECK (how IN ('exact', 'suggested', 'suggested_auto', 'manual')),
  by           text NOT NULL,
  reasons      jsonb NOT NULL DEFAULT '[]',
  undoes       uuid UNIQUE REFERENCES customer_invoice_payments(id),
  created_at   timestamptz NOT NULL DEFAULT now()
);
-- One payment is counted towards an invoice once.
CREATE UNIQUE INDEX customer_invoice_payments_once ON customer_invoice_payments (invoice_id, payment_id) WHERE undoes IS NULL;
CREATE INDEX customer_invoice_payments_invoice ON customer_invoice_payments (invoice_id);
ALTER TABLE customer_invoice_payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_invoice_payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_invoice_payments TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON customer_invoice_payments TO taskiem_app;

-- An invoice's status, from what it is and what was paid.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_invoice_status() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  paid bigint;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF OLD.voided_at IS NOT NULL AND (NEW.voided_at IS DISTINCT FROM OLD.voided_at OR NEW.sent_at IS DISTINCT FROM OLD.sent_at) THEN
      RAISE EXCEPTION 'invoice % is void', OLD.number USING ERRCODE = '23514';
    END IF;
    IF OLD.sent_at IS NOT NULL AND NEW.sent_at IS NULL THEN
      RAISE EXCEPTION 'invoice % was sent', OLD.number USING ERRCODE = '23514';
    END IF;
    -- What was sent to the customer is not changed afterwards.
    IF OLD.sent_at IS NOT NULL AND (NEW.lines IS DISTINCT FROM OLD.lines OR NEW.total_kobo IS DISTINCT FROM OLD.total_kobo
        OR NEW.customer IS DISTINCT FROM OLD.customer OR NEW.currency IS DISTINCT FROM OLD.currency) THEN
      RAISE EXCEPTION 'invoice % was sent and cannot be changed; void it and send another', OLD.number USING ERRCODE = '23514';
    END IF;
  END IF;
  SELECT COALESCE(sum(p.amount_kobo), 0) INTO paid FROM customer_invoice_payments p
   WHERE p.invoice_id = NEW.id AND p.undoes IS NULL
     AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id);
  NEW.paid_kobo := paid;
  IF paid >= NEW.total_kobo AND NEW.paid_at IS NULL THEN
    NEW.paid_at := now();
  ELSIF paid < NEW.total_kobo THEN
    NEW.paid_at := NULL;
  END IF;
  NEW.status := CASE
    WHEN NEW.voided_at IS NOT NULL THEN 'void'
    WHEN paid >= NEW.total_kobo THEN 'paid'
    WHEN NEW.sent_at IS NULL THEN 'draft'
    WHEN NEW.due_on IS NOT NULL AND NEW.due_on < (now() AT TIME ZONE 'Africa/Lagos')::date THEN 'overdue'
    WHEN paid > 0 THEN 'part_paid'
    ELSE 'sent' END;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER customer_invoice_status BEFORE INSERT OR UPDATE ON customer_invoices
  FOR EACH ROW EXECUTE FUNCTION taskiem_customer_invoice_status();

-- A payment (or its undoing) moves its invoice: the update recomputes it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_invoice_paid() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  target uuid := NEW.invoice_id;
BEGIN
  IF NEW.undoes IS NOT NULL THEN
    SELECT p.invoice_id INTO target FROM customer_invoice_payments p WHERE p.id = NEW.undoes AND p.undoes IS NULL;
    IF target IS NULL OR target <> NEW.invoice_id THEN
      RAISE EXCEPTION 'an undo must name a payment of the same invoice' USING ERRCODE = '23514';
    END IF;
  ELSIF EXISTS (SELECT 1 FROM customer_invoices i WHERE i.id = NEW.invoice_id AND i.sent_at IS NULL AND i.voided_at IS NULL) THEN
    RAISE EXCEPTION 'a draft invoice takes no payment' USING ERRCODE = '23514';
  END IF;
  UPDATE customer_invoices SET updated_at = now() WHERE id = NEW.invoice_id;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER customer_invoice_paid AFTER INSERT ON customer_invoice_payments
  FOR EACH ROW EXECUTE FUNCTION taskiem_customer_invoice_paid();

-- The daily overdue check: touching an invoice recomputes its status.
-- Runs for every tenant, so it is the dispatch role's.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_invoices_overdue()
RETURNS TABLE (tenant_id uuid, id uuid, number text)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE customer_invoices i SET updated_at = now()
   WHERE i.status IN ('sent', 'part_paid') AND i.due_on < (now() AT TIME ZONE 'Africa/Lagos')::date
  RETURNING i.tenant_id, i.id, i.number
$$;
-- +goose StatementEnd

-- What an invoice's page shows, for its token: nothing personal.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_customer_invoice(p_token_hash bytea)
RETURNS TABLE (tenant_id uuid, number text, status text, lines jsonb, total_kobo bigint, paid_kobo bigint, currency text, due_on date, note text, sent_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT i.tenant_id, i.number, i.status, i.lines, i.total_kobo, i.paid_kobo, i.currency, i.due_on, i.note, i.sent_at
    FROM customer_invoices i JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE i.page_token_hash = p_token_hash AND i.sent_at IS NOT NULL
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_customer_invoices_overdue() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_customer_invoice(bytea) OWNER TO taskiem_dispatch;
GRANT UPDATE (updated_at) ON customer_invoices TO taskiem_dispatch;
GRANT SELECT ON customer_invoices TO taskiem_dispatch;
CREATE POLICY dispatch ON customer_invoices TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON customer_invoice_payments FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON customer_invoice_payments TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_customer_invoices_overdue(), taskiem_public_customer_invoice(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_customer_invoices_overdue() TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_public_customer_invoice(bytea) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_customer_invoice(bytea);
DROP FUNCTION taskiem_customer_invoices_overdue();
DROP TABLE customer_invoice_payments;
DROP TABLE customer_invoices;
DROP TABLE customer_invoice_settings;
DROP FUNCTION taskiem_customer_invoice_paid();
DROP FUNCTION taskiem_customer_invoice_status();
