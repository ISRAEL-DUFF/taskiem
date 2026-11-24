-- Linked businesses in chat under the name and logo they chose (the business
-- profile, 00399): the chat list, a conversation's head and its info show a
-- linked business by its profile name and logo, as a contact is shown by its
-- name and photo. Only for a business we have an active link with; a rejected
-- logo is never shown. Bills, invoices and orders keep the registered name.
-- +goose Up
CREATE POLICY dispatch ON storefront_media FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, urls, moderation) ON storefront_media TO taskiem_dispatch;

-- The profile name and whether there is a logo, for each business we are
-- actively linked with and that has said something.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_profiles() RETURNS TABLE (business uuid, name text, has_logo boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.id, bp.name, (m.id IS NOT NULL AND m.moderation <> 'rejected')
    FROM business_links l
    JOIN tenants o ON o.id = CASE WHEN l.a_tenant = taskiem_link_self() THEN l.b_tenant ELSE l.a_tenant END
    JOIN business_profile bp ON bp.tenant_id = o.id
    LEFT JOIN storefront_media m ON m.id = bp.logo_media AND m.tenant_id = o.id
   WHERE l.status = 'active' AND taskiem_link_self() IN (l.a_tenant, l.b_tenant)
$$;
-- +goose StatementEnd

-- A linked business's logo: the sizes it was stored at, or nothing (not
-- linked, no logo, or one that was refused: the same answer for each).
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_logo(p_business uuid) RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT m.urls
    FROM business_links l
    JOIN business_profile bp ON bp.tenant_id = p_business
    JOIN storefront_media m ON m.id = bp.logo_media AND m.tenant_id = p_business AND m.moderation <> 'rejected'
   WHERE l.status = 'active' AND p_business <> taskiem_link_self()
     AND l.a_tenant = LEAST(taskiem_link_self(), p_business) AND l.b_tenant = GREATEST(taskiem_link_self(), p_business)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_link_profiles()', 'taskiem_link_logo(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_link_logo(uuid);
DROP FUNCTION taskiem_link_profiles();
REVOKE SELECT (id, tenant_id, urls, moderation) ON storefront_media FROM taskiem_dispatch;
DROP POLICY dispatch ON storefront_media;
