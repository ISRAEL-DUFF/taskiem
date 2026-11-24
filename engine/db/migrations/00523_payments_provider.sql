-- The payments account a payment was prepared on (its channel, as
-- connector/connection): its bank code is that account's, so a payment
-- prepared on one account is never sent from another, and an account
-- "changed since last paid" is judged within the same account only.
-- Payments prepared before this have none, and are prepared again.
-- +goose Up
ALTER TABLE payments ADD COLUMN provider_channel text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE payments DROP COLUMN provider_channel;
