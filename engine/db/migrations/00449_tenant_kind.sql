-- Taskiem for everyone §18 (docs/spec/taskiem-for-everyone.md, decision
-- 0068): what a tenant was started as, a business or a workflows
-- workspace. A workflows workspace has every area off and is never found
-- by other businesses (Explore, the links search). The kind is set when the
-- tenant is made and follows its areas after: turning any area on makes it
-- a business, turning every area off makes it workflows again. Existing
-- tenants are businesses.
-- +goose Up
ALTER TABLE tenants ADD COLUMN kind text NOT NULL DEFAULT 'business' CHECK (kind IN ('business', 'workflows'));
-- The finding definers (owned by taskiem_dispatch) read it.
GRANT SELECT (kind) ON tenants TO taskiem_dispatch;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_findable(p_tenant uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT ls.findable FROM link_settings ls WHERE ls.tenant_id = p_tenant),
                  EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_tenant AND s.status = 'published'))
     AND EXISTS (SELECT 1 FROM tenants t WHERE t.id = p_tenant AND t.status = 'active' AND t.kind = 'business')
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
     AND NOT EXISTS (SELECT 1 FROM business_areas b WHERE b.tenant_id = p_tenant AND b.area = 'links' AND NOT b.enabled)
$$;
-- +goose StatementEnd
REVOKE SELECT (kind) ON tenants FROM taskiem_dispatch;
ALTER TABLE tenants DROP COLUMN kind;
