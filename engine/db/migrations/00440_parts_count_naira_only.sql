-- Only naira is counted (docs/matching.md, docs/money-log.md). The money
-- log keeps each entry's currency as the rail gave it, and nothing read
-- it afterwards: a $50 charge (5000 cents) could be suggested for, and
-- counted towards, a ₦50 invoice (5000 kobo). Matching now leaves money
-- in another currency for a person, a person's match of it is refused,
-- and the reports sum naira alone; here the database refuses a payment
-- part of such an entry too, whoever writes it. An undo is still taken.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_part_in_naira() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.entry_id IS NOT NULL AND NEW.undoes IS NULL
     AND EXISTS (SELECT 1 FROM money_entries e WHERE e.id = NEW.entry_id AND e.currency <> 'NGN') THEN
    RAISE EXCEPTION 'money in another currency is not counted as naira' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER invoice_payment_in_naira BEFORE INSERT ON customer_invoice_payments
  FOR EACH ROW EXECUTE FUNCTION taskiem_money_part_in_naira();
CREATE TRIGGER order_payment_in_naira BEFORE INSERT ON storefront_order_payments
  FOR EACH ROW EXECUTE FUNCTION taskiem_money_part_in_naira();

-- +goose Down
DROP TRIGGER order_payment_in_naira ON storefront_order_payments;
DROP TRIGGER invoice_payment_in_naira ON customer_invoice_payments;
DROP FUNCTION taskiem_money_part_in_naira();
