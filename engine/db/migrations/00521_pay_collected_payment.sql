-- The payment a collected row became, while its batch waits: so a batch
-- cancelled after some of its rows were sent gives back only the rows
-- that were not, and a row held out of a batch stays on the list.
-- +goose Up
ALTER TABLE pay_collected ADD COLUMN payment_id uuid REFERENCES payments(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE pay_collected DROP COLUMN payment_id;
