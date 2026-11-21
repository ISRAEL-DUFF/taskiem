-- Saved objects S2 (docs/spec/saved-objects.md §6.3): "deliver order
-- P-7QX4KD to @warehouse". A purchase from a linked supplier may say where
-- it is delivered, sealed as the business's default is
-- (stock_settings.deliver_to): set while it has not yet reached the
-- supplier, and copied into the supplier's order in its place. NULL: the
-- business's default.
-- +goose Up
ALTER TABLE purchase_orders ADD COLUMN deliver_to jsonb CHECK (deliver_to IS NULL OR jsonb_typeof(deliver_to) = 'object');

-- +goose Down
ALTER TABLE purchase_orders DROP COLUMN deliver_to;
