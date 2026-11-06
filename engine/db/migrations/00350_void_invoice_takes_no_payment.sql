-- A void invoice takes no new payment (docs/invoices.md: "Void ... it
-- takes no new payment"). Only a draft was refused, so money paid into a
-- voided invoice's account number was counted towards it and its money-log
-- entry marked matched: absorbed, never in To match. The api refuses it
-- (invoicing.CountPayment, ErrState) and leaves the money for a person;
-- the database refuses it too, whoever writes. An undo of a payment counted
-- before the void is still taken.
-- +goose Up
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
  ELSIF EXISTS (SELECT 1 FROM customer_invoices i WHERE i.id = NEW.invoice_id AND i.sent_at IS NULL AND i.recorded_at IS NULL AND i.voided_at IS NULL) THEN
    RAISE EXCEPTION 'a draft invoice takes no payment' USING ERRCODE = '23514';
  END IF;
  UPDATE customer_invoices SET updated_at = now() WHERE id = NEW.invoice_id;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
