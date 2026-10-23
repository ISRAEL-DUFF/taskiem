-- Matching money to invoices (decision 0045 point 6; docs/spec/
-- invoices-and-money-log.md 8, T5; docs/matching.md).
--
-- A match is still a row of customer_invoice_payments (00197): inserted
-- once, undone by a row naming it. It now names the money log entry it
-- counts, and one entry is counted towards one invoice at a time. The
-- matcher remembers which entries it has looked at (money_match_state)
-- and what it suggested (money_suggestions); a rejected suggestion is not
-- offered again. Payments are copied to the kept table money_matches, as
-- entries are to money_log.
-- +goose Up
ALTER TABLE customer_invoice_payments ADD COLUMN entry_id uuid REFERENCES money_entries(id);
ALTER TABLE customer_invoice_payments ADD COLUMN mirrored_at timestamptz;
-- One entry counts towards one invoice at a time: checked by the writer
-- under a lock on the entry (an undone match stays, so no index can say it).
CREATE INDEX customer_invoice_payments_entry ON customer_invoice_payments (entry_id) WHERE entry_id IS NOT NULL;
CREATE INDEX customer_invoice_payments_unmirrored ON customer_invoice_payments (created_at) WHERE mirrored_at IS NULL;
GRANT UPDATE (mirrored_at) ON customer_invoice_payments TO taskiem_app;
-- The same payment counts towards an invoice once at a time; an undone
-- payment's row stays, so it may count again. 00197's unique index said
-- "once ever"; the writer checks under a lock instead.
DROP INDEX customer_invoice_payments_once;
CREATE INDEX customer_invoice_payments_ref ON customer_invoice_payments (invoice_id, payment_id);
-- mirrored_at is set once; nothing else about a payment ever changes.
-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_payment_kept() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.mirrored_at IS NOT NULL OR NEW.mirrored_at IS NULL
     OR (to_jsonb(NEW) - 'mirrored_at') IS DISTINCT FROM (to_jsonb(OLD) - 'mirrored_at') THEN
    RAISE EXCEPTION 'a payment is never changed; undo it instead' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER invoice_payment_kept BEFORE UPDATE ON customer_invoice_payments
  FOR EACH ROW EXECUTE FUNCTION taskiem_invoice_payment_kept();

ALTER TABLE customer_invoice_settings ADD COLUMN auto_match boolean NOT NULL DEFAULT false;

-- What the matcher made of each money-in entry.
CREATE TABLE money_match_state (
  entry_id      uuid PRIMARY KEY REFERENCES money_entries(id),
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  state         text NOT NULL CHECK (state IN ('matched', 'suggested', 'unmatched')),
  considered_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX money_match_state_open ON money_match_state (tenant_id, state);
ALTER TABLE money_match_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_match_state FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_match_state TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON money_match_state TO taskiem_app;

CREATE TABLE money_suggestions (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  entry_id    uuid NOT NULL REFERENCES money_entries(id),
  invoice_id  uuid NOT NULL REFERENCES customer_invoices(id),
  score       int NOT NULL CHECK (score BETWEEN 0 AND 100),
  reasons     jsonb NOT NULL DEFAULT '[]',
  status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'confirmed', 'rejected', 'closed')),
  decided_by  text,
  decided_at  timestamptz,
  payment_id  uuid REFERENCES customer_invoice_payments(id), -- the match it became
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (entry_id, invoice_id)
);
CREATE INDEX money_suggestions_open ON money_suggestions (tenant_id, created_at DESC) WHERE status = 'open';
ALTER TABLE money_suggestions ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_suggestions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_suggestions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE (status, decided_by, decided_at, payment_id) ON money_suggestions TO taskiem_app;

-- Tenants with money-in entries the matcher has not looked at yet, and
-- with payments to copy to money_matches.
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_unconsidered_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT e.tenant_id FROM money_entries e
   WHERE e.direction = 'in' AND e.status = 'settled' AND e.kind IN ('payment', 'account_credit', 'pos_sale', 'statement', 'cash')
     AND NOT EXISTS (SELECT 1 FROM money_match_state s WHERE s.entry_id = e.id)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_unmirrored_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT p.tenant_id FROM customer_invoice_payments p WHERE p.mirrored_at IS NULL
$$;
-- +goose StatementEnd
CREATE POLICY dispatch ON money_match_state FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON money_match_state TO taskiem_dispatch;
ALTER FUNCTION taskiem_money_unconsidered_tenants() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_money_unmirrored_payment_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_money_unconsidered_tenants(), taskiem_money_unmirrored_payment_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_money_unconsidered_tenants(), taskiem_money_unmirrored_payment_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_money_unmirrored_payment_tenants();
DROP FUNCTION taskiem_money_unconsidered_tenants();
REVOKE SELECT ON money_match_state FROM taskiem_dispatch;
DROP TABLE money_suggestions;
DROP TABLE money_match_state;
ALTER TABLE customer_invoice_settings DROP COLUMN auto_match;
DROP TRIGGER invoice_payment_kept ON customer_invoice_payments;
DROP FUNCTION taskiem_invoice_payment_kept();
REVOKE UPDATE (mirrored_at) ON customer_invoice_payments FROM taskiem_app;
DROP INDEX customer_invoice_payments_ref;
CREATE UNIQUE INDEX customer_invoice_payments_once ON customer_invoice_payments (invoice_id, payment_id) WHERE undoes IS NULL;
DROP INDEX customer_invoice_payments_unmirrored;
DROP INDEX customer_invoice_payments_entry;
ALTER TABLE customer_invoice_payments DROP COLUMN mirrored_at;
ALTER TABLE customer_invoice_payments DROP COLUMN entry_id;
