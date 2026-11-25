-- An address on an invoice (build plan H6, g-saved-addr): the customer
-- envelope (customer_invoices.customer, sealed under the customer's
-- subject) gains the customer's address, printed on the invoice and its
-- PDF and filled from a saved person's or business's place when the
-- invoice is made with @name. The envelope is sealed JSON, so its shape is
-- the application's (invoicing.Customer); nothing is in production, so no
-- row is rewritten: an envelope without an address reads as one with
-- none. This says what the envelope holds where the schema is read.
-- +goose Up
COMMENT ON COLUMN customer_invoices.customer IS 'Sealed under subject_id: {name, phone, email, address, key} (invoicing.Customer)';

-- +goose Down
COMMENT ON COLUMN customer_invoices.customer IS NULL;
