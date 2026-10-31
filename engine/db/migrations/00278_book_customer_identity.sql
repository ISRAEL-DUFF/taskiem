-- Who a customer of the debtor book is, as their invoices group (decision
-- 0060, R5; invoicing.BookCustomer.CustomerID): customer_invoices.
-- customer_id. A customer recorded with no phone is their record's own
-- key, key:book:<id>, which their lines carry. One recorded with a phone,
-- or one chosen from a sent invoice when a name is looked up, is the
-- personal-data subject of that phone, as an invoice sent to it is: their
-- lines carry no key, so a notebook line and an invoice sent to the same
-- number are one balance. A phone's customer has one record.
-- +goose Up
ALTER TABLE invoice_customers ADD COLUMN customer_id text;
UPDATE invoice_customers SET customer_id = 'key:book:' || id::text;
ALTER TABLE invoice_customers ALTER COLUMN customer_id SET NOT NULL;
ALTER TABLE invoice_customers ADD CONSTRAINT invoice_customers_customer_id_check
  CHECK (customer_id = 'key:book:' || id::text OR (customer_id = subject_id AND subject_id LIKE 'phone:%'));
CREATE UNIQUE INDEX invoice_customers_customer ON invoice_customers (tenant_id, customer_id);

-- +goose Down
DROP INDEX invoice_customers_customer;
ALTER TABLE invoice_customers DROP CONSTRAINT invoice_customers_customer_id_check;
ALTER TABLE invoice_customers DROP COLUMN customer_id;
