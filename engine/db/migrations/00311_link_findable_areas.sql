-- Business areas (docs/spec/business-areas.md): a business that has turned
-- Linked businesses off is not found by other businesses' searches, so no
-- one asks it to link with them where it cannot see them. Its links stay as
-- they are and come back with the switch. A business with no row is found
-- as before: its default is on (engine/areas DefaultOn); if that default
-- is ever changed, change it here too.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_findable(p_tenant uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT ls.findable FROM link_settings ls WHERE ls.tenant_id = p_tenant),
                  EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_tenant AND s.status = 'published'))
     AND EXISTS (SELECT 1 FROM tenants t WHERE t.id = p_tenant AND t.status = 'active')
     AND NOT EXISTS (SELECT 1 FROM business_areas b WHERE b.tenant_id = p_tenant AND b.area = 'links' AND NOT b.enabled)
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_findable(p_tenant uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT ls.findable FROM link_settings ls WHERE ls.tenant_id = p_tenant),
                  EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_tenant AND s.status = 'published'))
     AND EXISTS (SELECT 1 FROM tenants t WHERE t.id = p_tenant AND t.status = 'active')
$$;
-- +goose StatementEnd
