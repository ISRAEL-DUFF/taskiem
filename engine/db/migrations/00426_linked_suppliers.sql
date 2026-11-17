-- A supplier that is a linked business needs no phone number: its orders
-- and bills go through the link, in the conversation (business links §7).
-- A supplier with neither a phone nor a link could not be reached at all.
-- +goose Up
ALTER TABLE suppliers ALTER COLUMN phone DROP NOT NULL;
ALTER TABLE suppliers ALTER COLUMN phone_hash DROP NOT NULL;
ALTER TABLE suppliers ADD CONSTRAINT suppliers_reachable
  CHECK ((phone IS NULL) = (phone_hash IS NULL) AND (phone IS NOT NULL OR link_id IS NOT NULL));

-- +goose Down
ALTER TABLE suppliers DROP CONSTRAINT suppliers_reachable;
ALTER TABLE suppliers ALTER COLUMN phone_hash SET NOT NULL;
ALTER TABLE suppliers ALTER COLUMN phone SET NOT NULL;
