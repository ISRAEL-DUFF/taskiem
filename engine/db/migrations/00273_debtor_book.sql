-- The debtor book over invoices (decision 0060; docs/spec/no-sheet-needed.md
-- §6.4, R5). A notebook debt ("Ada owes 5,000") is an invoice of kind
-- credit, recorded: open and owed (matching, attribution oldest first,
-- the list, the statement and its page count it) but never sent; it moves
-- to part-paid and paid by payments as a sent one does, to overdue with a
-- due date, and to sent only when the business sends it.
--
-- Its customer may have no phone or email: invoice_customers keeps a
-- customer of the book under a key of its own (book:<id>), the name
-- sealed under a subject of the customer's own, beside a lower-cased
-- search form of it. That search form is a recorded exception to the
-- personal-data rules (Q8; docs/security/threat-model.md): read only by
-- the name lookup, never in outputs, audit or anything sent to a model,
-- and erased with the customer (the trigger on subject_keys below).
--
-- The credit terms are the owner's: credit_days (none for No fixed time)
-- and credit_terms_at, when they were answered. Until then no credit line
-- has a due date.
-- +goose Up
ALTER TABLE customer_invoices ADD COLUMN kind text NOT NULL DEFAULT 'invoice' CHECK (kind IN ('invoice', 'credit'));
ALTER TABLE customer_invoices ADD COLUMN recorded_at timestamptz;
ALTER TABLE customer_invoices ADD CONSTRAINT customer_invoices_credit_recorded CHECK (kind = 'invoice' OR recorded_at IS NOT NULL);
ALTER TABLE customer_invoices DROP CONSTRAINT customer_invoices_status_check;
ALTER TABLE customer_invoices ADD CONSTRAINT customer_invoices_status_check
  CHECK (status IN ('draft', 'recorded', 'sent', 'part_paid', 'paid', 'overdue', 'void'));
DROP INDEX customer_invoices_due;
CREATE INDEX customer_invoices_due ON customer_invoices (due_on) WHERE status IN ('recorded', 'sent', 'part_paid');

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_invoice_status() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  paid bigint;
  n_old int;
  amended boolean := false;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF OLD.voided_at IS NOT NULL AND (NEW.voided_at IS DISTINCT FROM OLD.voided_at OR NEW.sent_at IS DISTINCT FROM OLD.sent_at
        OR NEW.lines IS DISTINCT FROM OLD.lines) THEN
      RAISE EXCEPTION 'invoice % is void', OLD.number USING ERRCODE = '23514';
    END IF;
    IF OLD.sent_at IS NOT NULL AND NEW.sent_at IS NULL THEN
      RAISE EXCEPTION 'invoice % was sent', OLD.number USING ERRCODE = '23514';
    END IF;
    IF OLD.recorded_at IS NOT NULL AND NEW.recorded_at IS DISTINCT FROM OLD.recorded_at THEN
      RAISE EXCEPTION 'invoice % was recorded', OLD.number USING ERRCODE = '23514';
    END IF;
    -- What was sent to the customer is not changed afterwards, but
    -- amended: lines added at the end, each a recorded amendment.
    IF OLD.sent_at IS NOT NULL AND (NEW.customer IS DISTINCT FROM OLD.customer OR NEW.currency IS DISTINCT FROM OLD.currency) THEN
      RAISE EXCEPTION 'invoice % was sent and cannot be changed; void it and send another', OLD.number USING ERRCODE = '23514';
    END IF;
    IF OLD.sent_at IS NOT NULL AND (NEW.lines IS DISTINCT FROM OLD.lines OR NEW.total_kobo IS DISTINCT FROM OLD.total_kobo) THEN
      n_old := jsonb_array_length(OLD.lines);
      IF jsonb_array_length(NEW.lines) <= n_old
         OR COALESCE((SELECT jsonb_agg(x.e ORDER BY x.i) FROM jsonb_array_elements(NEW.lines) WITH ORDINALITY x(e, i) WHERE x.i <= n_old), '[]'::jsonb) IS DISTINCT FROM OLD.lines THEN
        RAISE EXCEPTION 'invoice % was sent and cannot be changed; amend it', OLD.number USING ERRCODE = '23514';
      END IF;
      IF EXISTS (SELECT 1 FROM jsonb_array_elements(NEW.lines) WITH ORDINALITY x(e, i) WHERE x.i > n_old AND NOT EXISTS (
            SELECT 1 FROM customer_invoice_amendments a WHERE a.invoice_id = NEW.id AND a.id::text = x.e->>'amendment' AND a.line = x.e))
         OR (SELECT count(*) FROM jsonb_array_elements(NEW.lines) e WHERE e ? 'amendment')
            <> (SELECT count(DISTINCT e->>'amendment') FROM jsonb_array_elements(NEW.lines) e WHERE e ? 'amendment') THEN
        RAISE EXCEPTION 'invoice % changes only by a recorded amendment, once', OLD.number USING ERRCODE = '23514';
      END IF;
      NEW.total_kobo := (SELECT COALESCE(sum((e->>'quantity')::bigint * (e->>'unit_kobo')::bigint), 0) FROM jsonb_array_elements(NEW.lines) e);
      amended := true;
    END IF;
  END IF;
  SELECT COALESCE(sum(p.amount_kobo), 0) INTO paid FROM customer_invoice_payments p
   WHERE p.invoice_id = NEW.id AND p.undoes IS NULL
     AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id);
  IF amended AND NEW.total_kobo < paid THEN
    RAISE EXCEPTION 'invoice %: the total cannot go below what was paid', OLD.number USING ERRCODE = '23514';
  END IF;
  NEW.paid_kobo := paid;
  IF paid >= NEW.total_kobo AND NEW.paid_at IS NULL THEN
    NEW.paid_at := now();
  ELSIF paid < NEW.total_kobo THEN
    NEW.paid_at := NULL;
  END IF;
  NEW.status := CASE
    WHEN NEW.voided_at IS NOT NULL THEN 'void'
    WHEN paid >= NEW.total_kobo THEN 'paid'
    WHEN NEW.sent_at IS NULL AND NEW.recorded_at IS NULL THEN 'draft'
    WHEN NEW.due_on IS NOT NULL AND NEW.due_on < (now() AT TIME ZONE 'Africa/Lagos')::date THEN 'overdue'
    WHEN paid > 0 THEN 'part_paid'
    WHEN NEW.sent_at IS NULL THEN 'recorded'
    ELSE 'sent' END;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- A recorded line takes payments: only a draft refuses them.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_invoice_paid() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  target uuid := NEW.invoice_id;
BEGIN
  IF NEW.undoes IS NOT NULL THEN
    SELECT p.invoice_id INTO target FROM customer_invoice_payments p WHERE p.id = NEW.undoes AND p.undoes IS NULL;
    IF target IS NULL OR target <> NEW.invoice_id THEN
      RAISE EXCEPTION 'an undo must name a payment of the same invoice' USING ERRCODE = '23514';
    END IF;
  ELSIF EXISTS (SELECT 1 FROM customer_invoices i WHERE i.id = NEW.invoice_id AND i.sent_at IS NULL AND i.recorded_at IS NULL AND i.voided_at IS NULL) THEN
    RAISE EXCEPTION 'a draft invoice takes no payment' USING ERRCODE = '23514';
  END IF;
  UPDATE customer_invoices SET updated_at = now() WHERE id = NEW.invoice_id;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- The daily overdue check takes recorded lines too.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_invoices_overdue()
RETURNS TABLE (tenant_id uuid, id uuid, number text)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE customer_invoices i SET updated_at = now()
   WHERE i.status IN ('recorded', 'sent', 'part_paid') AND i.due_on < (now() AT TIME ZONE 'Africa/Lagos')::date
  RETURNING i.tenant_id, i.id, i.number
$$;
-- +goose StatementEnd

-- A recorded line's page (a receipt's link) shows it, and takes payment.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_customer_invoice(p_token_hash bytea)
RETURNS TABLE (tenant_id uuid, number text, status text, lines jsonb, total_kobo bigint, paid_kobo bigint, currency text, due_on date, note text, sent_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT i.tenant_id, i.number, i.status, i.lines, i.total_kobo, i.paid_kobo, i.currency, i.due_on, i.note, i.sent_at
    FROM customer_invoices i JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE i.page_token_hash = p_token_hash AND (i.sent_at IS NOT NULL OR i.recorded_at IS NOT NULL)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_invoice_request(p_token_hash bytea, p_kind text, p_amount_kobo bigint)
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
   WHERE i.page_token_hash = p_token_hash AND i.status IN ('recorded', 'sent', 'part_paid', 'overdue') FOR UPDATE OF i;
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_invoice_payment(p_token_hash bytea)
RETURNS TABLE (kind text, status text, amount_kobo bigint, account_number text, bank_name text, account_name text, payment_url text,
               expires_at timestamptz, on_late text, on_wrong_amount text, waiting boolean, issued_last_hour int, issued int, part_payments boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH inv AS (
    SELECT i.id, i.allow_part_payments FROM customer_invoices i JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
     WHERE i.page_token_hash = p_token_hash AND (i.sent_at IS NOT NULL OR i.recorded_at IS NOT NULL)
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

CREATE TABLE invoice_customers (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  subject_id  text NOT NULL,                 -- the customer's own personal-data subject
  name        jsonb NOT NULL,                -- sealed under subject_id
  search      text NOT NULL CHECK (length(search) BETWEEN 1 AND 120),
  phone       jsonb,                         -- sealed, once known
  email       jsonb,                         -- sealed, once known
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX invoice_customers_search ON invoice_customers (tenant_id, search);
CREATE INDEX invoice_customers_subject ON invoice_customers (tenant_id, subject_id);
ALTER TABLE invoice_customers ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_customers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_customers TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON invoice_customers TO taskiem_app;
GRANT UPDATE (phone, email, updated_at) ON invoice_customers TO taskiem_app;

-- A customer erased (their subject's key shredded) loses the search form
-- of their name with it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_customers_erased() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  UPDATE invoice_customers SET search = '(erased)', updated_at = now() WHERE tenant_id = NEW.tenant_id AND subject_id = NEW.subject_id;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER invoice_customers_erased AFTER UPDATE OF shredded_at ON subject_keys
  FOR EACH ROW WHEN (OLD.shredded_at IS NULL AND NEW.shredded_at IS NOT NULL) EXECUTE FUNCTION taskiem_invoice_customers_erased();
REVOKE EXECUTE ON FUNCTION taskiem_invoice_customers_erased() FROM PUBLIC;

ALTER TABLE customer_invoice_settings ADD COLUMN credit_days int CHECK (credit_days BETWEEN 1 AND 365);
ALTER TABLE customer_invoice_settings ADD COLUMN credit_terms_at timestamptz;

-- +goose Down
ALTER TABLE customer_invoice_settings DROP COLUMN credit_terms_at;
ALTER TABLE customer_invoice_settings DROP COLUMN credit_days;
DROP TRIGGER invoice_customers_erased ON subject_keys;
DROP FUNCTION taskiem_invoice_customers_erased();
DROP TABLE invoice_customers;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_invoice_payment(p_token_hash bytea)
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_invoice_request(p_token_hash bytea, p_kind text, p_amount_kobo bigint)
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_customer_invoice(p_token_hash bytea)
RETURNS TABLE (tenant_id uuid, number text, status text, lines jsonb, total_kobo bigint, paid_kobo bigint, currency text, due_on date, note text, sent_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT i.tenant_id, i.number, i.status, i.lines, i.total_kobo, i.paid_kobo, i.currency, i.due_on, i.note, i.sent_at
    FROM customer_invoices i JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE i.page_token_hash = p_token_hash AND i.sent_at IS NOT NULL
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_invoices_overdue()
RETURNS TABLE (tenant_id uuid, id uuid, number text)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE customer_invoices i SET updated_at = now()
   WHERE i.status IN ('sent', 'part_paid') AND i.due_on < (now() AT TIME ZONE 'Africa/Lagos')::date
  RETURNING i.tenant_id, i.id, i.number
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_invoice_paid() RETURNS trigger
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
-- Recorded lines go with the kind: they are deleted (a down migration on
-- a database holding notebook debts loses them, as the table it adds).
DELETE FROM customer_invoice_payments WHERE invoice_id IN (SELECT id FROM customer_invoices WHERE kind = 'credit');
DELETE FROM customer_invoices WHERE kind = 'credit';
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_invoice_status() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  paid bigint;
  n_old int;
  amended boolean := false;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF OLD.voided_at IS NOT NULL AND (NEW.voided_at IS DISTINCT FROM OLD.voided_at OR NEW.sent_at IS DISTINCT FROM OLD.sent_at
        OR NEW.lines IS DISTINCT FROM OLD.lines) THEN
      RAISE EXCEPTION 'invoice % is void', OLD.number USING ERRCODE = '23514';
    END IF;
    IF OLD.sent_at IS NOT NULL AND NEW.sent_at IS NULL THEN
      RAISE EXCEPTION 'invoice % was sent', OLD.number USING ERRCODE = '23514';
    END IF;
    -- What was sent to the customer is not changed afterwards, but
    -- amended: lines added at the end, each a recorded amendment.
    IF OLD.sent_at IS NOT NULL AND (NEW.customer IS DISTINCT FROM OLD.customer OR NEW.currency IS DISTINCT FROM OLD.currency) THEN
      RAISE EXCEPTION 'invoice % was sent and cannot be changed; void it and send another', OLD.number USING ERRCODE = '23514';
    END IF;
    IF OLD.sent_at IS NOT NULL AND (NEW.lines IS DISTINCT FROM OLD.lines OR NEW.total_kobo IS DISTINCT FROM OLD.total_kobo) THEN
      n_old := jsonb_array_length(OLD.lines);
      IF jsonb_array_length(NEW.lines) <= n_old
         OR COALESCE((SELECT jsonb_agg(x.e ORDER BY x.i) FROM jsonb_array_elements(NEW.lines) WITH ORDINALITY x(e, i) WHERE x.i <= n_old), '[]'::jsonb) IS DISTINCT FROM OLD.lines THEN
        RAISE EXCEPTION 'invoice % was sent and cannot be changed; amend it', OLD.number USING ERRCODE = '23514';
      END IF;
      IF EXISTS (SELECT 1 FROM jsonb_array_elements(NEW.lines) WITH ORDINALITY x(e, i) WHERE x.i > n_old AND NOT EXISTS (
            SELECT 1 FROM customer_invoice_amendments a WHERE a.invoice_id = NEW.id AND a.id::text = x.e->>'amendment' AND a.line = x.e))
         OR (SELECT count(*) FROM jsonb_array_elements(NEW.lines) e WHERE e ? 'amendment')
            <> (SELECT count(DISTINCT e->>'amendment') FROM jsonb_array_elements(NEW.lines) e WHERE e ? 'amendment') THEN
        RAISE EXCEPTION 'invoice % changes only by a recorded amendment, once', OLD.number USING ERRCODE = '23514';
      END IF;
      NEW.total_kobo := (SELECT COALESCE(sum((e->>'quantity')::bigint * (e->>'unit_kobo')::bigint), 0) FROM jsonb_array_elements(NEW.lines) e);
      amended := true;
    END IF;
  END IF;
  SELECT COALESCE(sum(p.amount_kobo), 0) INTO paid FROM customer_invoice_payments p
   WHERE p.invoice_id = NEW.id AND p.undoes IS NULL
     AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id);
  IF amended AND NEW.total_kobo < paid THEN
    RAISE EXCEPTION 'invoice %: the total cannot go below what was paid', OLD.number USING ERRCODE = '23514';
  END IF;
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
DROP INDEX customer_invoices_due;
CREATE INDEX customer_invoices_due ON customer_invoices (due_on) WHERE status IN ('sent', 'part_paid');
ALTER TABLE customer_invoices DROP CONSTRAINT customer_invoices_status_check;
ALTER TABLE customer_invoices ADD CONSTRAINT customer_invoices_status_check
  CHECK (status IN ('draft', 'sent', 'part_paid', 'paid', 'overdue', 'void'));
ALTER TABLE customer_invoices DROP CONSTRAINT customer_invoices_credit_recorded;
ALTER TABLE customer_invoices DROP COLUMN recorded_at;
ALTER TABLE customer_invoices DROP COLUMN kind;
