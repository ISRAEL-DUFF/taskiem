-- Pages G1, Taskiem's own records as a page's source (docs/spec/pages.md §5;
-- decision 0072; engine/pages/records.go): a page narrows the business's
-- orders and invoices by status, newest first, and an order's view shows its
-- latest delivery. Indexes for those; nothing else changes.
-- +goose Up
CREATE INDEX storefront_orders_status ON storefront_orders (tenant_id, status, created_at DESC);
CREATE INDEX customer_invoices_status ON customer_invoices (tenant_id, status, created_at DESC);
CREATE INDEX deliveries_order ON deliveries (order_id, offered_at DESC);

-- +goose Down
DROP INDEX deliveries_order;
DROP INDEX customer_invoices_status;
DROP INDEX storefront_orders_status;
