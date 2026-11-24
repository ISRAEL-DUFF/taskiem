-- A stock item's barcode (device fields D2, docs/spec/device-fields.md
-- §16): Scan to count reads it. It was a column of the catalogue table
-- before stock moved off it (decision 0070); it is the stock item's own
-- now, so a material has one too.
--
-- - stock_items.code: up to 64 characters, no control characters; none
--   (NULL) for an item without one.
-- - One current stock item per business has a code (as said, any case):
--   a retired one keeps its code, and a scan never finds it.
-- - The app changes it as it changes an item's other details. The count's
--   trigger (taskiem_stock_item_kept) refuses only a count changed outside
--   a movement, so it lets the code change; the code is not copied to the
--   kept tables, so it does not mark the item to copy again (updated_at).
-- - Nothing outside the business reads it: the public page's functions
--   (taskiem_product_left…) name their columns, and the dispatch role has
--   no grant on it.
-- +goose Up
ALTER TABLE stock_items ADD COLUMN code text
  CHECK (code IS NULL OR (length(code) BETWEEN 1 AND 64 AND code !~ '[[:cntrl:]]'));
CREATE UNIQUE INDEX stock_items_code ON stock_items (tenant_id, lower(code)) WHERE code IS NOT NULL AND retired_at IS NULL;
GRANT UPDATE (code) ON stock_items TO taskiem_app;

-- +goose Down
REVOKE UPDATE (code) ON stock_items FROM taskiem_app;
DROP INDEX stock_items_code;
ALTER TABLE stock_items DROP COLUMN code;
