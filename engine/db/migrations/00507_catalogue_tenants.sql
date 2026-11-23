-- Every business with a catalogue (docs/spec/what-a-business-buys.md §4.3,
-- §11 As built: PR review fix). Taskiem is not in production: nothing is
-- carried over.
--
-- - taskiem_catalogue_tenants lists the active businesses whose storefront
--   names a catalogue table, whatever the page's status: the old-columns
--   check runs once for each, so an owner with only a draft page (not in
--   taskiem_storefronts_due) is told too.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.catalogue_table IS NOT NULL
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_catalogue_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_catalogue_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_catalogue_tenants();
