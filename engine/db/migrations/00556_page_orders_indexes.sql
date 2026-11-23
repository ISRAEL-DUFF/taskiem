-- +goose NO TRANSACTION
-- Pages (docs/spec/pages.md §5, §9): a page narrows the business's orders by
-- status, newest first (the orders record view), and an open page asks which
-- orders changed in the last few seconds (changed_at, 00555). storefront_orders
-- is large, so both are built CONCURRENTLY (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS storefront_orders_status ON storefront_orders (tenant_id, status, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS storefront_orders_changed_at ON storefront_orders (tenant_id, changed_at);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS storefront_orders_changed_at;
DROP INDEX CONCURRENTLY IF EXISTS storefront_orders_status;
