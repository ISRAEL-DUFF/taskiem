-- +goose NO TRANSACTION
-- Pages (docs/spec/pages.md §5): the orders record view shows each order's
-- latest delivery. deliveries is large, so the index is built CONCURRENTLY
-- (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS deliveries_order ON deliveries (order_id, offered_at DESC);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS deliveries_order;
