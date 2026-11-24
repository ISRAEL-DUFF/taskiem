-- An invoice counts no more than its total (docs/matching.md#more-than-is-due).
-- Only a draft and a void invoice were refused, and nothing capped a
-- payment at what was due, so a second payment into a paid invoice's
-- account number was counted in full: the invoice showed twice its total
-- paid, and the money was neither the customer's credit nor in To match.
-- The api now counts at most what is due, under the invoice's row lock
-- (invoicing.CountPayment), and keeps the rest as the customer's credit
-- (money_entry_customers, how invoice: paid into the account Taskiem gave
-- for an invoice of theirs) or leaves it for a person; the database
-- refuses a payment that would take an invoice past its total too,
-- whoever writes. An undo is still taken, so an invoice the old code
-- over-counted can be put right by undoing a payment; none is rewritten
-- here (the leaks check "overpaid" lists them).
-- +goose Up
ALTER TABLE money_entry_customers DROP CONSTRAINT money_entry_customers_how_check;
ALTER TABLE money_entry_customers ADD CONSTRAINT money_entry_customers_how_check CHECK (how IN ('account', 'person', 'link', 'invoice'));
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
  ELSIF EXISTS (SELECT 1 FROM customer_invoices i WHERE i.id = NEW.invoice_id AND i.voided_at IS NOT NULL) THEN
    RAISE EXCEPTION 'a void invoice takes no payment' USING ERRCODE = '23514';
  ELSIF EXISTS (SELECT 1 FROM customer_invoices i WHERE i.id = NEW.invoice_id AND i.sent_at IS NULL AND i.recorded_at IS NULL AND i.voided_at IS NULL) THEN
    RAISE EXCEPTION 'a draft invoice takes no payment' USING ERRCODE = '23514';
  END IF;
  -- Locks the invoice and works its paid amount out again
  -- (taskiem_customer_invoice_status), so the check below sees every
  -- payment, one writer at a time.
  UPDATE customer_invoices SET updated_at = now() WHERE id = NEW.invoice_id;
  IF NEW.undoes IS NULL AND EXISTS (SELECT 1 FROM customer_invoices i WHERE i.id = NEW.invoice_id AND i.paid_kobo > i.total_kobo) THEN
    RAISE EXCEPTION 'an invoice takes no more than is due on it' USING ERRCODE = '23514';
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose Down
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
  ELSIF EXISTS (SELECT 1 FROM customer_invoices i WHERE i.id = NEW.invoice_id AND i.voided_at IS NOT NULL) THEN
    RAISE EXCEPTION 'a void invoice takes no payment' USING ERRCODE = '23514';
  ELSIF EXISTS (SELECT 1 FROM customer_invoices i WHERE i.id = NEW.invoice_id AND i.sent_at IS NULL AND i.recorded_at IS NULL AND i.voided_at IS NULL) THEN
    RAISE EXCEPTION 'a draft invoice takes no payment' USING ERRCODE = '23514';
  END IF;
  UPDATE customer_invoices SET updated_at = now() WHERE id = NEW.invoice_id;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
DELETE FROM money_entry_customers WHERE how = 'invoice';
ALTER TABLE money_entry_customers DROP CONSTRAINT money_entry_customers_how_check;
ALTER TABLE money_entry_customers ADD CONSTRAINT money_entry_customers_how_check CHECK (how IN ('account', 'person', 'link'));
