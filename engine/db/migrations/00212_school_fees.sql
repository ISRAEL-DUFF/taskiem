-- Fees and term invoices (decision 0046 points 3 and 11; docs/spec/
-- schools.md 5, B3; docs/schools.md#fees): a class's fee structure for a
-- term, a student's chosen optional items and standing discounts, the
-- link from a term invoice to its student and term, the school's
-- discount threshold, and amendments to sent invoices (every business).
--
-- The rules this schema keeps:
--   - a fee item's name is unique in its class and term; a past term's
--     fees are read-only;
--   - a student is invoiced for a term once: one school_term_invoices row
--     per term and student, and one per invoice;
--   - a sent invoice changes only by amendment: its lines may only grow,
--     each new line naming a recorded amendment of that invoice (never
--     twice), and its total is then the sum of its lines, never below
--     what was paid. Amendments are inserted and never changed.
-- +goose Up
ALTER TABLE school_settings ADD COLUMN discount_threshold_kobo bigint CHECK (discount_threshold_kobo >= 0);

CREATE TABLE school_fee_items (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  term_id           uuid NOT NULL REFERENCES school_terms(id),
  class_id          uuid NOT NULL REFERENCES school_classes(id),
  name              text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 80),
  amount_kobo       bigint NOT NULL CHECK (amount_kobo BETWEEN 100 AND 100000000000),
  kind              text NOT NULL CHECK (kind IN ('required', 'optional')),
  pass_through_for  text CHECK (length(btrim(pass_through_for)) BETWEEN 1 AND 60),
  position          int NOT NULL DEFAULT 0,
  created_by        text NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX school_fee_items_name ON school_fee_items (term_id, class_id, lower(name));
CREATE INDEX school_fee_items_term ON school_fee_items (tenant_id, term_id, class_id);
ALTER TABLE school_fee_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_fee_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_fee_items TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON school_fee_items TO taskiem_app;
GRANT UPDATE (name, amount_kobo, kind, pass_through_for, position, updated_at) ON school_fee_items TO taskiem_app;

-- A past term's fees are its history.
-- +goose StatementBegin
CREATE FUNCTION taskiem_school_fee_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  t record;
BEGIN
  SELECT started_at, current, name INTO t FROM school_terms
   WHERE id = CASE WHEN TG_OP = 'DELETE' THEN OLD.term_id ELSE NEW.term_id END;
  IF t.started_at IS NOT NULL AND NOT t.current THEN
    RAISE EXCEPTION 'term % is past: its fees are read-only', t.name USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'UPDATE' AND (NEW.term_id IS DISTINCT FROM OLD.term_id OR NEW.class_id IS DISTINCT FROM OLD.class_id) THEN
    RAISE EXCEPTION 'a fee item stays in its class and term' USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER school_fee_guard BEFORE INSERT OR UPDATE OR DELETE ON school_fee_items
  FOR EACH ROW EXECUTE FUNCTION taskiem_school_fee_guard();

-- The optional items a student takes in a term (transport, lessons).
CREATE TABLE school_student_options (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  student_id   uuid NOT NULL REFERENCES school_students(id),
  fee_item_id  uuid NOT NULL REFERENCES school_fee_items(id) ON DELETE CASCADE,
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (student_id, fee_item_id)
);
CREATE INDEX school_student_options_item ON school_student_options (fee_item_id);
ALTER TABLE school_student_options ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_student_options FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_student_options TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON school_student_options TO taskiem_app;

-- A student's standing discounts: a percent of the school's own fees or
-- an amount, with a reason, until ended.
CREATE TABLE school_discounts (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  student_id   uuid NOT NULL REFERENCES school_students(id),
  kind         text NOT NULL CHECK (kind IN ('sibling', 'staff_child', 'scholarship', 'hardship', 'other')),
  percent      int CHECK (percent BETWEEN 1 AND 100),
  amount_kobo  bigint CHECK (amount_kobo BETWEEN 100 AND 100000000000),
  reason       text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 200),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  ended_at     timestamptz,
  ended_by     text,
  CHECK ((percent IS NULL) <> (amount_kobo IS NULL))
);
CREATE INDEX school_discounts_student ON school_discounts (tenant_id, student_id) WHERE ended_at IS NULL;
ALTER TABLE school_discounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_discounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_discounts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_discounts TO taskiem_app;
GRANT UPDATE (ended_at, ended_by) ON school_discounts TO taskiem_app;

-- A term invoice: the invoice of one student for one term, once.
CREATE TABLE school_term_invoices (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  term_id      uuid NOT NULL REFERENCES school_terms(id),
  student_id   uuid NOT NULL REFERENCES school_students(id),
  class_id     uuid NOT NULL REFERENCES school_classes(id),
  invoice_id   uuid NOT NULL UNIQUE REFERENCES customer_invoices(id),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  mirrored_at  timestamptz,                -- the invoice's updated_at last copied to the kept table
  PRIMARY KEY (term_id, student_id)
);
CREATE INDEX school_term_invoices_class ON school_term_invoices (tenant_id, term_id, class_id);
CREATE INDEX school_term_invoices_student ON school_term_invoices (student_id);
ALTER TABLE school_term_invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_term_invoices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_term_invoices TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_term_invoices TO taskiem_app;
GRANT UPDATE (mirrored_at) ON school_term_invoices TO taskiem_app;

-- invoice_term in a workflow: what one step's call did, so a retry of
-- the step answers the same.
CREATE TABLE school_term_invoicings (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  key         text NOT NULL CHECK (length(key) BETWEEN 1 AND 200),
  term_id     uuid NOT NULL REFERENCES school_terms(id),
  result      jsonb NOT NULL,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, key)
);
ALTER TABLE school_term_invoicings ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_term_invoicings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_term_invoicings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_term_invoicings TO taskiem_app;
GRANT UPDATE (result) ON school_term_invoicings TO taskiem_app;

-- An amendment of a sent invoice (any business): a line added, a line
-- taken out (a negative line naming it) or a discount, with a reason.
CREATE TABLE customer_invoice_amendments (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  invoice_id   uuid NOT NULL REFERENCES customer_invoices(id),
  kind         text NOT NULL CHECK (kind IN ('add', 'remove', 'discount')),
  line         jsonb NOT NULL CHECK (jsonb_typeof(line) = 'object'),
  amount_kobo  bigint NOT NULL CHECK (amount_kobo <> 0),
  removes      int CHECK (removes >= 0),
  reason       text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 300),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'remove') = (removes IS NOT NULL)),
  CHECK (kind = 'add' AND amount_kobo > 0 OR kind <> 'add' AND amount_kobo < 0)
);
CREATE INDEX customer_invoice_amendments_invoice ON customer_invoice_amendments (invoice_id, created_at);
-- A line is taken out once.
CREATE UNIQUE INDEX customer_invoice_amendments_removes ON customer_invoice_amendments (invoice_id, removes) WHERE removes IS NOT NULL;
ALTER TABLE customer_invoice_amendments ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_invoice_amendments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_invoice_amendments TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON customer_invoice_amendments TO taskiem_app;
CREATE POLICY dispatch ON customer_invoice_amendments FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON customer_invoice_amendments TO taskiem_dispatch;

-- An invoice's status, from what it is and what was paid; a sent
-- invoice's lines change only by amendment.
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

-- The kept copy of the term's fees (school_term_fees): the businesses
-- with something not yet copied, students and classes as before.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_schools_unmirrored_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM school_settings s
   WHERE s.enabled
     AND (EXISTS (SELECT 1 FROM school_students x WHERE x.tenant_id = s.tenant_id AND x.mirrored_at IS DISTINCT FROM x.updated_at)
       OR EXISTS (SELECT 1 FROM school_classes c WHERE c.tenant_id = s.tenant_id AND c.mirrored_at IS DISTINCT FROM c.updated_at)
       OR EXISTS (SELECT 1 FROM school_term_invoices ti JOIN customer_invoices i ON i.id = ti.invoice_id
                   WHERE ti.tenant_id = s.tenant_id AND ti.mirrored_at IS DISTINCT FROM i.updated_at))
$$;
-- +goose StatementEnd
CREATE POLICY dispatch ON school_term_invoices FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, invoice_id, mirrored_at) ON school_term_invoices TO taskiem_dispatch;

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_schools_unmirrored_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM school_settings s
   WHERE s.enabled
     AND (EXISTS (SELECT 1 FROM school_students x WHERE x.tenant_id = s.tenant_id AND x.mirrored_at IS DISTINCT FROM x.updated_at)
       OR EXISTS (SELECT 1 FROM school_classes c WHERE c.tenant_id = s.tenant_id AND c.mirrored_at IS DISTINCT FROM c.updated_at))
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_invoice_status() RETURNS trigger
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
DROP TABLE customer_invoice_amendments;
DROP TABLE school_term_invoicings;
DROP TABLE school_term_invoices;
DROP TABLE school_discounts;
DROP TABLE school_student_options;
DROP TABLE school_fee_items;
DROP FUNCTION taskiem_school_fee_guard();
ALTER TABLE school_settings DROP COLUMN discount_threshold_kobo;
