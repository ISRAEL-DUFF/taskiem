-- Pay a bill by transfer, in the chat (the payments flow, part 1;
-- docs/spec/chat-payments-flow.md): a buyer asks for an account number
-- for a bill, as a request on the seller's invoice (channel bill), and the
-- number the seller's provider gives comes back on the bill, with the
-- link mirror. A bill keeps its newest number (transfer), when the buyer
-- last asked (transfer_asked_at) and why a number could not be had
-- (transfer_problem); whether the seller's provider gives numbers at all
-- (pay_by_transfer).
-- +goose Up
ALTER TABLE customer_invoice_requests DROP CONSTRAINT customer_invoice_requests_channel_check,
  ADD CONSTRAINT customer_invoice_requests_channel_check CHECK (channel IN ('page', 'whatsapp', 'telegram', 'bill'));
ALTER TABLE bills ADD COLUMN transfer jsonb,
  ADD COLUMN transfer_asked_at timestamptz,
  ADD COLUMN transfer_problem text,
  ADD COLUMN pay_by_transfer boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE bills DROP COLUMN pay_by_transfer, DROP COLUMN transfer_problem, DROP COLUMN transfer_asked_at, DROP COLUMN transfer;
DELETE FROM customer_invoice_requests WHERE channel = 'bill';
ALTER TABLE customer_invoice_requests DROP CONSTRAINT customer_invoice_requests_channel_check,
  ADD CONSTRAINT customer_invoice_requests_channel_check CHECK (channel IN ('page', 'whatsapp', 'telegram'));
