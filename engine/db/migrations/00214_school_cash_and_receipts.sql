-- Cash at the desk, the second person and receipts (decision 0046;
-- docs/spec/schools.md 8 and 9, B5; docs/schools.md#cash-and-transfers,
-- #receipts), and the invoice trigger's payment event (every business;
-- docs/invoices.md#starting-a-workflow-when-an-invoice-is-paid).
--
-- school_pending_postings holds what waits for a second person (and, so
-- the Payments page can list them with their state, what was posted at
-- once): a cash or transfer posting, a standing discount, a discount
-- amendment or a term invoice held for its discount. A request is
-- decided once, under its row's lock, by someone other than its
-- requester; nothing it asks for reaches the fees until then.
--
-- school_receipts is one receipt per payment part counted towards a
-- school's term invoice: numbered per business and term from a locked
-- counter (school_receipt_counters), issued once per part (the part is
-- the key), voided and never deleted when the part is undone. Each
-- carries its own unguessable token, by whose hash the public host
-- answers Valid or Voided and nothing more.
--
-- invoice_payment_starts is the outbox of the invoice trigger's payment
-- event, as invoice_paid_starts (00209) is of paid: one row per part and
-- workflow, written in the transaction that counts the part.
-- +goose Up
ALTER TABLE school_settings ADD COLUMN cash_threshold_kobo bigint CHECK (cash_threshold_kobo >= 0);

-- A school's request waiting for a second person is sent to approvers on
-- their linked chat app, as a run's approval is (api/whatsapp_school_approvals.go).
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match', 'money_daily', 'school_approval'));

CREATE TABLE school_pending_postings (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  kind            text NOT NULL CHECK (kind IN ('cash_posting', 'discount', 'discount_amendment', 'term_invoice')),
  status          text NOT NULL CHECK (status IN ('pending', 'posted', 'declined')),
  student_id      uuid REFERENCES school_students(id),
  invoice_id      uuid REFERENCES customer_invoices(id),
  term_id         uuid REFERENCES school_terms(id),
  amount_kobo     bigint NOT NULL CHECK (amount_kobo >= 0),
  details         jsonb NOT NULL DEFAULT '{}',    -- what was asked: how, date, note, the discount, the amendment
  key             text CHECK (length(key) BETWEEN 1 AND 200),  -- the caller's Idempotency-Key
  requested_by    text NOT NULL,
  requested_user  uuid REFERENCES users(id),
  requested_at    timestamptz NOT NULL DEFAULT now(),
  decided_by      text,
  decided_user    uuid REFERENCES users(id),
  decided_at      timestamptz,
  decision_reason text CHECK (length(decision_reason) <= 300),
  result          jsonb,                          -- what posting it did: the entry, the parts, the receipts
  CHECK (status = 'pending' OR decided_at IS NOT NULL OR kind = 'cash_posting'),
  CHECK (decided_user IS NULL OR requested_user IS NULL OR decided_user <> requested_user)
);
CREATE UNIQUE INDEX school_pending_postings_key ON school_pending_postings (tenant_id, key) WHERE key IS NOT NULL;
CREATE UNIQUE INDEX school_pending_postings_term_once ON school_pending_postings (term_id, student_id) WHERE kind = 'term_invoice' AND status = 'pending';
CREATE INDEX school_pending_postings_open ON school_pending_postings (tenant_id, requested_at) WHERE status = 'pending';
CREATE INDEX school_pending_postings_student ON school_pending_postings (tenant_id, student_id, requested_at DESC);
ALTER TABLE school_pending_postings ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_pending_postings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_pending_postings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_pending_postings TO taskiem_app;
GRANT UPDATE (status, decided_by, decided_user, decided_at, decision_reason, result) ON school_pending_postings TO taskiem_app;

-- A decided request is never decided again, and a decision never changes.
-- +goose StatementBegin
CREATE FUNCTION taskiem_school_posting_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF OLD.status <> 'pending' AND (NEW.status IS DISTINCT FROM OLD.status OR NEW.decided_by IS DISTINCT FROM OLD.decided_by
      OR NEW.decided_at IS DISTINCT FROM OLD.decided_at OR NEW.decision_reason IS DISTINCT FROM OLD.decision_reason) THEN
    RAISE EXCEPTION 'the request was decided already' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER school_posting_guard BEFORE UPDATE ON school_pending_postings
  FOR EACH ROW EXECUTE FUNCTION taskiem_school_posting_guard();

-- Receipt numbers: one counter per business and term, locked by its row.
CREATE TABLE school_receipt_counters (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  term_id    uuid NOT NULL REFERENCES school_terms(id),
  next       int NOT NULL DEFAULT 1 CHECK (next >= 1),
  PRIMARY KEY (tenant_id, term_id)
);
ALTER TABLE school_receipt_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_receipt_counters FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_receipt_counters TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON school_receipt_counters TO taskiem_app;

CREATE TABLE school_receipts (
  id                  uuid PRIMARY KEY,
  tenant_id           uuid NOT NULL REFERENCES tenants(id),
  number              text NOT NULL CHECK (number ~ '^RCT-[0-9]{4}T[0-9]+-[0-9]{4,}$'),
  term_id             uuid NOT NULL REFERENCES school_terms(id),
  student_id          uuid NOT NULL REFERENCES school_students(id),
  class_id            uuid NOT NULL REFERENCES school_classes(id),
  invoice_id          uuid NOT NULL REFERENCES customer_invoices(id),
  part_id             uuid NOT NULL UNIQUE REFERENCES customer_invoice_payments(id),  -- issued once per part
  amount_kobo         bigint NOT NULL CHECK (amount_kobo > 0),
  balance_after_kobo  bigint NOT NULL CHECK (balance_after_kobo >= 0),
  how                 text NOT NULL,              -- the part's: exact, suggested, suggested_auto, manual
  paid_at             timestamptz NOT NULL,       -- when the money came (the entry's), else when it was counted
  token_hash          bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  token               jsonb NOT NULL,             -- the token itself, sealed
  issued_by           text NOT NULL,
  issued_at           timestamptz NOT NULL DEFAULT now(),
  voided_at           timestamptz,
  void_reason         text CHECK (length(void_reason) <= 200),
  UNIQUE (tenant_id, number)
);
CREATE INDEX school_receipts_student ON school_receipts (tenant_id, student_id, issued_at DESC);
CREATE INDEX school_receipts_term ON school_receipts (tenant_id, term_id, issued_at DESC);
CREATE INDEX school_receipts_invoice ON school_receipts (invoice_id);
ALTER TABLE school_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_receipts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_receipts TO taskiem_app;
GRANT UPDATE (voided_at, void_reason) ON school_receipts TO taskiem_app;

-- A receipt is voided, never deleted or renumbered.
-- +goose StatementBegin
CREATE FUNCTION taskiem_school_receipt_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'a receipt is voided, never deleted' USING ERRCODE = '23514';
  END IF;
  IF NEW.number <> OLD.number OR NEW.part_id <> OLD.part_id OR NEW.amount_kobo <> OLD.amount_kobo OR NEW.token_hash <> OLD.token_hash THEN
    RAISE EXCEPTION 'a receipt is never changed' USING ERRCODE = '23514';
  END IF;
  IF OLD.voided_at IS NOT NULL AND (NEW.voided_at IS DISTINCT FROM OLD.voided_at OR NEW.void_reason IS DISTINCT FROM OLD.void_reason) THEN
    RAISE EXCEPTION 'a voided receipt stays voided' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER school_receipt_guard BEFORE UPDATE OR DELETE ON school_receipts
  FOR EACH ROW EXECUTE FUNCTION taskiem_school_receipt_guard();

-- What a receipt's verification page shows, for its token: Valid or
-- Voided, the school, the amount and the date. Nothing else.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_school_receipt(p_token_hash bytea)
RETURNS TABLE (tenant_id uuid, amount_kobo bigint, paid_at timestamptz, voided boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT r.tenant_id, r.amount_kobo, r.paid_at, r.voided_at IS NOT NULL
    FROM school_receipts r JOIN tenants t ON t.id = r.tenant_id AND t.status = 'active'
   WHERE r.token_hash = p_token_hash
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_school_receipt(bytea) OWNER TO taskiem_dispatch;
GRANT SELECT ON school_receipts TO taskiem_dispatch;
CREATE POLICY dispatch ON school_receipts FOR SELECT TO taskiem_dispatch USING (true);
REVOKE EXECUTE ON FUNCTION taskiem_public_school_receipt(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_school_receipt(bytea) TO taskiem_public;

-- The invoice trigger's payment event: one start per part and workflow.
CREATE TABLE invoice_payment_starts (
  part_id        uuid NOT NULL REFERENCES customer_invoice_payments(id),
  workflow_id    uuid NOT NULL,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  invoice_id     uuid NOT NULL REFERENCES customer_invoices(id),
  version        int NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until  timestamptz,
  attempts       int NOT NULL DEFAULT 0,
  run_id         uuid,
  note           text,
  done_at        timestamptz,
  PRIMARY KEY (part_id, workflow_id)
);
CREATE INDEX invoice_payment_starts_open ON invoice_payment_starts (created_at) WHERE done_at IS NULL;
ALTER TABLE invoice_payment_starts ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_payment_starts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_payment_starts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON invoice_payment_starts TO taskiem_app;
CREATE POLICY dispatch ON invoice_payment_starts TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON invoice_payment_starts TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_payment_starts_claim(p_limit int)
RETURNS TABLE (tenant_id uuid, part_id uuid, invoice_id uuid, workflow_id uuid, version int, attempts int)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE invoice_payment_starts s SET claimed_until = now() + interval '1 minute', attempts = s.attempts + 1
   WHERE (s.part_id, s.workflow_id) IN (
     SELECT x.part_id, x.workflow_id FROM invoice_payment_starts x
      WHERE x.done_at IS NULL AND (x.claimed_until IS NULL OR x.claimed_until < now())
      ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING s.tenant_id, s.part_id, s.invoice_id, s.workflow_id, s.version, s.attempts
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_payment_starts_done(p_part uuid, p_workflow uuid, p_run uuid, p_note text)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE invoice_payment_starts SET done_at = now(), claimed_until = NULL, run_id = p_run, note = p_note
   WHERE part_id = p_part AND workflow_id = p_workflow AND done_at IS NULL
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_invoice_payment_starts_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_invoice_payment_starts_done(uuid, uuid, uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_invoice_payment_starts_claim(int), taskiem_invoice_payment_starts_done(uuid, uuid, uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_invoice_payment_starts_claim(int), taskiem_invoice_payment_starts_done(uuid, uuid, uuid, text) TO taskiem_app;

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind = 'school_approval';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match', 'money_daily'));
DROP FUNCTION taskiem_invoice_payment_starts_done(uuid, uuid, uuid, text);
DROP FUNCTION taskiem_invoice_payment_starts_claim(int);
DROP TABLE invoice_payment_starts;
DROP FUNCTION taskiem_public_school_receipt(bytea);
DROP TRIGGER school_receipt_guard ON school_receipts;
DROP FUNCTION taskiem_school_receipt_guard();
DROP TABLE school_receipts;
DROP TABLE school_receipt_counters;
DROP TRIGGER school_posting_guard ON school_pending_postings;
DROP FUNCTION taskiem_school_posting_guard();
DROP TABLE school_pending_postings;
ALTER TABLE school_settings DROP COLUMN cash_threshold_kobo;
