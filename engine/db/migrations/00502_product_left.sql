-- How many are left of a product, S1b (docs/spec/what-a-business-buys.md
-- §4.1, §8; decision 0070). Taskiem is not in production: nothing is
-- carried over.
--
-- - taskiem_product_left(tenant, product ids): for each product of the
--   business counted by a stock item, how many whole sales the stock item
--   covers (its count over the product's uses, rounded down, never below
--   0). Nothing else: no cost, level, supplier or the stock item itself;
--   a product counted by none is not in it. The public page (taskiem_public)
--   reads it for a business with a page online; the app role
--   (taskiem_app) for the business in scope, or one actively linked to it
--   (a linked buyer's price lists and their shop).
-- - The catalogue copy and a linked buyer's copy of a supplier's prices
--   no longer hold a count: how many are left is read with them, so a
--   sale writes no copy. supplier_prices.left_count goes.
-- +goose Up
GRANT SELECT (product_id, stock_item_id, uses) ON product_stock TO taskiem_dispatch;
GRANT SELECT (id, count) ON stock_items TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_product_left(p_tenant uuid, p_products text[])
RETURNS TABLE (product_id text, sales_left bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.product_id, GREATEST(i.count, 0) / p.uses
    FROM product_stock p
    JOIN stock_items i ON i.tenant_id = p.tenant_id AND i.id = p.stock_item_id
    JOIN tenants t ON t.id = p.tenant_id AND t.status = 'active'
   WHERE p.tenant_id = p_tenant
     AND p.product_id = ANY (p_products[1:2000])
     AND (p_tenant = ANY (taskiem_tenant_scope())
          OR EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_tenant AND s.status IN ('published', 'closed'))
          OR EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                      AND ((l.a_tenant = p_tenant AND l.b_tenant = ANY (taskiem_tenant_scope()))
                        OR (l.b_tenant = p_tenant AND l.a_tenant = ANY (taskiem_tenant_scope())))))
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_product_left(uuid, text[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_product_left(uuid, text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_product_left(uuid, text[]) TO taskiem_public, taskiem_app;

ALTER TABLE supplier_prices DROP COLUMN left_count;

-- +goose Down
ALTER TABLE supplier_prices ADD COLUMN left_count bigint CHECK (left_count IS NULL OR left_count >= 0);
DROP FUNCTION taskiem_product_left(uuid, text[]);
REVOKE SELECT (id, count) ON stock_items FROM taskiem_dispatch;
REVOKE SELECT (product_id, stock_item_id, uses) ON product_stock FROM taskiem_dispatch;
