-- How many are left, read by the public page, only of what it sells (PR
-- review of S1a–U3; docs/spec/what-a-business-buys.md §4.1, §8). Taskiem is
-- not in production: nothing is carried over.
--
-- - taskiem_product_left answered the public role for any product id of a
--   business with a page online, a product taken off sale among them. The
--   public role now reads it only of the products the page's copy holds
--   on sale (storefront_catalogue, any of its tables, available), and of
--   their sizes and colours. The business itself and a linked buyer read
--   it as before.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_product_left(p_tenant uuid, p_products text[])
RETURNS TABLE (product_id text, sales_left bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH on_sale AS (
    SELECT c.row_id AS id, c.item
      FROM storefront_catalogue c
      JOIN storefronts s ON s.id = c.storefront_id AND s.status IN ('published', 'closed')
     WHERE s.tenant_id = p_tenant AND COALESCE((c.item->>'available')::boolean, false)
  ), shown AS (
    SELECT id FROM on_sale
    UNION
    SELECT z->>'id' FROM on_sale, jsonb_array_elements(CASE WHEN jsonb_typeof(item->'sizes') = 'array' THEN item->'sizes' ELSE '[]'::jsonb END) z
  )
  SELECT p.product_id, GREATEST(i.count, 0) / p.uses
    FROM product_stock p
    JOIN stock_items i ON i.tenant_id = p.tenant_id AND i.id = p.stock_item_id
    JOIN tenants t ON t.id = p.tenant_id AND t.status = 'active'
   WHERE p.tenant_id = p_tenant
     AND p.product_id = ANY (p_products[1:2000])
     AND (p_tenant = ANY (taskiem_tenant_scope())
          OR p.product_id IN (SELECT id FROM shown)
          OR EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                      AND ((l.a_tenant = p_tenant AND l.b_tenant = ANY (taskiem_tenant_scope()))
                        OR (l.b_tenant = p_tenant AND l.a_tenant = ANY (taskiem_tenant_scope())))))
$$;
-- +goose StatementEnd
GRANT SELECT (row_id) ON storefront_catalogue TO taskiem_dispatch;

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_product_left(p_tenant uuid, p_products text[])
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
REVOKE SELECT (row_id) ON storefront_catalogue FROM taskiem_dispatch;
