-- +goose NO TRANSACTION
-- Pages (docs/spec/pages.md §5, §9): a page narrows the business's invoices
-- by status, newest first (the invoices record view), and an open page asks
-- which invoices changed in the last few seconds (changed_at, 00564).
-- customer_invoices is large, so both are built CONCURRENTLY
-- (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS customer_invoices_status ON customer_invoices (tenant_id, status, created_at DESC);
CREATE INDEX CONCURRENTLY IF NOT EXISTS customer_invoices_changed_at ON customer_invoices (tenant_id, changed_at);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS customer_invoices_changed_at;
DROP INDEX CONCURRENTLY IF EXISTS customer_invoices_status;
