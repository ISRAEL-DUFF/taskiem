-- Fronts F3 (docs/spec/fronts.md §5.1, §5.2): fronts made and removed in
-- the web app. A front that has taken nothing is removed with its
-- actions; each list block names the table its copy is kept by.

-- +goose Up
GRANT DELETE ON storefront_actions TO taskiem_app;
UPDATE storefront_blocks b SET config = b.config || jsonb_build_object('table_name', d.name)
  FROM data_tables d WHERE b.kind = 'list' AND d.id::text = b.config->>'table' AND NOT b.config ? 'table_name';

-- The home page's catalogue is its catalogue table's copy; each other
-- list is read by its table (one copy per table, §5.2).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_storefront_catalogue(p_storefront uuid)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(jsonb_agg(c.item ORDER BY c.position), '[]')
    FROM storefront_catalogue c
    JOIN storefronts s ON s.id = c.storefront_id AND s.status IN ('published', 'closed')
   WHERE c.storefront_id = p_storefront
     AND c.table_name = COALESCE((SELECT b.config->>'table_name' FROM storefront_blocks b JOIN storefront_fronts f ON f.id = b.front_id
                                   WHERE f.storefront_id = p_storefront AND f.slug = '' AND b.kind = 'list' AND b.config->>'role' = 'catalogue' LIMIT 1), '')
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_storefront_list(p_storefront uuid, p_table text)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(jsonb_agg(c.item ORDER BY c.position), '[]')
    FROM storefront_catalogue c
    JOIN storefronts s ON s.id = c.storefront_id AND s.status IN ('published', 'closed')
   WHERE c.storefront_id = p_storefront AND c.table_name = p_table
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_storefront_list(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_storefront_list(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_storefront_list(uuid, text) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_storefront_list(uuid, text);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_storefront_catalogue(p_storefront uuid)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(jsonb_agg(c.item ORDER BY c.position), '[]')
    FROM storefront_catalogue c
    JOIN storefronts s ON s.id = c.storefront_id AND s.status IN ('published', 'closed')
   WHERE c.storefront_id = p_storefront
$$;
-- +goose StatementEnd
-- Only the catalogue's copy is read before F3: the others go.
DELETE FROM storefront_catalogue c
 WHERE c.table_name <> COALESCE((SELECT b.config->>'table_name' FROM storefront_blocks b JOIN storefront_fronts f ON f.id = b.front_id
                                  WHERE f.storefront_id = c.storefront_id AND f.slug = '' AND b.kind = 'list' AND b.config->>'role' = 'catalogue' LIMIT 1), '');
UPDATE storefront_blocks SET config = config - 'table_name' WHERE kind = 'list';
REVOKE DELETE ON storefront_actions FROM taskiem_app;
