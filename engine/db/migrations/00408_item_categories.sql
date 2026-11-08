-- Product categories (docs/spec/chat-categories-and-variants.md, Part A):
-- an item on a price list carries the seller's category, and so does the
-- buyer's mirror, so a buyer's order sheet groups and filters as the
-- seller's does. The category itself lives in the catalogue table.
-- +goose Up
ALTER TABLE price_list_items ADD COLUMN category text NOT NULL DEFAULT '' CHECK (length(category) <= 60);
ALTER TABLE supplier_prices ADD COLUMN category text NOT NULL DEFAULT '' CHECK (length(category) <= 60);

-- +goose Down
ALTER TABLE supplier_prices DROP COLUMN category;
ALTER TABLE price_list_items DROP COLUMN category;
