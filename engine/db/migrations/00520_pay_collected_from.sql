-- Who sent the message a collected row came from, as the chat named them
-- ("from Funke's message"), so the list says it on every device.
-- +goose Up
ALTER TABLE pay_collected ADD COLUMN source_from text NOT NULL DEFAULT '' CHECK (length(source_from) <= 80);

-- +goose Down
ALTER TABLE pay_collected DROP COLUMN source_from;
