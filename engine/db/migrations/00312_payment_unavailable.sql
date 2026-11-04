-- Paying an invoice from its page when the business has no payments
-- connection that can open account numbers or card links (decision 0045):
-- the api role found no rail, so it left nothing at all, and the page
-- looked as if the button did nothing. It now leaves an account row with
-- status 'unavailable' (no payment id of a rail's; a made-up one), which the
-- page words as "online payment is not set up" and tries again when the
-- customer asks again, and which tells the business.
-- +goose Up
ALTER TABLE customer_invoice_accounts DROP CONSTRAINT customer_invoice_accounts_status_check;
ALTER TABLE customer_invoice_accounts ADD CONSTRAINT customer_invoice_accounts_status_check
  CHECK (status IN ('requested', 'open', 'failed', 'paid', 'returned', 'unavailable'));

-- +goose Down
DELETE FROM customer_invoice_accounts WHERE status = 'unavailable';
ALTER TABLE customer_invoice_accounts DROP CONSTRAINT customer_invoice_accounts_status_check;
ALTER TABLE customer_invoice_accounts ADD CONSTRAINT customer_invoice_accounts_status_check
  CHECK (status IN ('requested', 'open', 'failed', 'paid', 'returned'));
