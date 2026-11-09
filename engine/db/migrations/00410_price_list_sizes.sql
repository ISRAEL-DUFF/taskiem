-- Sizes and colours in trade (docs/spec/chat-categories-and-variants.md,
-- phase 4): a product sold in them is listed once, its choices and its sizes
-- on sale on its row ({"choices": [...], "sizes": [{item_id, name, variant,
-- price_kobo, available}]}), on the seller's list and the buyer's copy.

-- +goose Up
ALTER TABLE price_list_items ADD COLUMN sizes jsonb CHECK (sizes IS NULL OR jsonb_typeof(sizes) = 'object');
ALTER TABLE supplier_prices ADD COLUMN sizes jsonb CHECK (sizes IS NULL OR jsonb_typeof(sizes) = 'object');

-- +goose Down
ALTER TABLE supplier_prices DROP COLUMN sizes;
ALTER TABLE price_list_items DROP COLUMN sizes;
