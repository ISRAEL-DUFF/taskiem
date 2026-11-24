-- +goose NO TRANSACTION
-- taskiem_storefront_orders_forget (00385) looks, every hour, for ended
-- orders not yet forgotten, oldest change first. This index holds only
-- those, so the hourly pass reads them and not every order. storefront_orders
-- is large, so the index is built CONCURRENTLY (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY storefront_orders_to_forget ON storefront_orders (updated_at)
  WHERE forgotten_at IS NULL AND status IN ('declined', 'expired', 'completed', 'cancelled', 'no_show', 'price_changed', 'refunded');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS storefront_orders_to_forget;
