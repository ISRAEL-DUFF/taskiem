-- A thing's sale, said again (docs/spec/what-a-business-buys.md §5.3, §11
-- As built: U3). Taskiem is not in production: nothing is carried over.
--
-- - kept_things.sale_key: the Idempotency-Key of the request that recorded
--   the thing's sale with a price. The same sale with the same key again
--   (a retry whose answer was lost) is answered with the row as it is,
--   not refused as recorded already.
-- +goose Up
ALTER TABLE kept_things ADD COLUMN sale_key text CHECK (length(sale_key) BETWEEN 1 AND 255);
GRANT UPDATE (sale_key) ON kept_things TO taskiem_app;

-- +goose Down
REVOKE UPDATE (sale_key) ON kept_things FROM taskiem_app;
ALTER TABLE kept_things DROP COLUMN sale_key;
