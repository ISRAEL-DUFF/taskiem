-- Starting a workflow when an invoice is paid (decision 0045;
-- docs/invoices.md#starting-a-workflow-when-an-invoice-is-paid;
-- docs/contracts/wd-v1.md, the invoice trigger).
--
-- A workflow whose trigger is {"type": "invoice", "config": {"event":
-- "paid"}} is registered in triggers as type 'invoice' when it is
-- published. When a payment makes an invoice paid, the transaction that
-- records the payment (engine/invoicing.RecordPayment, the one place
-- every payment goes through) also writes one row here for each prod
-- workflow of the tenant registered then: the outbox. A row per invoice
-- and workflow, never another, so an invoice paid, undone and paid again,
-- a replayed payment or a restart starts no second run. The API server
-- takes the rows within a second and starts each run, deduplicated again
-- by the run's trigger receipt (invoice/<workflow>, the invoice's id), and
-- keeps the row with the run it started or why it started none.
-- +goose Up
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd', 'storefront', 'form', 'email', 'invoice'));

CREATE TABLE invoice_paid_starts (
  invoice_id     uuid NOT NULL REFERENCES customer_invoices(id),
  workflow_id    uuid NOT NULL,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  version        int NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until  timestamptz,
  attempts       int NOT NULL DEFAULT 0,
  run_id         uuid,
  note           text,
  done_at        timestamptz,
  PRIMARY KEY (invoice_id, workflow_id)
);
CREATE INDEX invoice_paid_starts_open ON invoice_paid_starts (created_at) WHERE done_at IS NULL;
ALTER TABLE invoice_paid_starts ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_paid_starts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_paid_starts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON invoice_paid_starts TO taskiem_app;
CREATE POLICY dispatch ON invoice_paid_starts TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON invoice_paid_starts TO taskiem_dispatch;

-- The api role takes rows waiting to start, a minute at a time; one that
-- failed is taken again after its claim runs out.
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_paid_starts_claim(p_limit int)
RETURNS TABLE (tenant_id uuid, invoice_id uuid, workflow_id uuid, version int, attempts int)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE invoice_paid_starts s SET claimed_until = now() + interval '1 minute', attempts = s.attempts + 1
   WHERE (s.invoice_id, s.workflow_id) IN (
     SELECT x.invoice_id, x.workflow_id FROM invoice_paid_starts x
      WHERE x.done_at IS NULL AND (x.claimed_until IS NULL OR x.claimed_until < now())
      ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING s.tenant_id, s.invoice_id, s.workflow_id, s.version, s.attempts
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_paid_starts_done(p_invoice uuid, p_workflow uuid, p_run uuid, p_note text)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE invoice_paid_starts SET done_at = now(), claimed_until = NULL, run_id = p_run, note = p_note
   WHERE invoice_id = p_invoice AND workflow_id = p_workflow AND done_at IS NULL
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_invoice_paid_starts_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_invoice_paid_starts_done(uuid, uuid, uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_invoice_paid_starts_claim(int), taskiem_invoice_paid_starts_done(uuid, uuid, uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_invoice_paid_starts_claim(int), taskiem_invoice_paid_starts_done(uuid, uuid, uuid, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_invoice_paid_starts_done(uuid, uuid, uuid, text);
DROP FUNCTION taskiem_invoice_paid_starts_claim(int);
DROP TABLE invoice_paid_starts;
DELETE FROM triggers WHERE type = 'invoice';
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd', 'storefront', 'form', 'email'));
