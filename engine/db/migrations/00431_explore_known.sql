-- Recently viewed, checked (docs/spec/mobile-app-explore-and-offline.md
-- §2.1): the businesses a phone or browser remembers having looked at,
-- answered as they are now, and only those that may still be shown (one
-- that chose to be found, or is linked with us; never ourselves, a
-- suspended page or one reported and not yet looked at). Nothing is told
-- of one that may not: it is simply not in the answer, as in a search.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore_known(p_ids uuid[])
RETURNS TABLE (business uuid, name text, slug text, area text, tagline text, about text, has_logo boolean, photos int, state text, ver text,
               delivers boolean, vehicle text, delivery_areas text[], categories text[])
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
BEGIN
  RETURN QUERY
  SELECT t.id,
         COALESCE(NULLIF(bp.name, ''), NULLIF(snap.model->'card'->>'name', ''), t.name),
         s.published_slug,
         COALESCE(NULLIF(bp.area, ''), snap.model->'card'->>'area'),
         COALESCE(NULLIF(bp.tagline, ''), snap.model->'card'->>'tagline'),
         bp.about,
         EXISTS (SELECT 1 FROM business_profile b2 JOIN storefront_media m ON m.id = b2.logo_media AND m.tenant_id = b2.tenant_id AND m.moderation <> 'rejected' WHERE b2.tenant_id = t.id),
         (SELECT count(*)::int FROM taskiem_link_photo_list(t.id)),
         COALESCE((SELECT taskiem_link_view(l, v_me) FROM business_links l WHERE l.a_tenant = LEAST(v_me, t.id) AND l.b_tenant = GREATEST(v_me, t.id)), 'none'),
         taskiem_link_explore_ver(t.id),
         COALESCE(bp.delivers, false), bp.vehicle, COALESCE(bp.delivery_areas, '{}'), COALESCE(bp.categories, '{}')
    FROM unnest(p_ids[1:10]) WITH ORDINALITY AS ask(id, n)
    JOIN tenants t ON t.id = ask.id AND t.status = 'active'
    LEFT JOIN business_profile bp ON bp.tenant_id = t.id
    LEFT JOIN storefronts s ON s.tenant_id = t.id AND s.status = 'published'
    LEFT JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
   WHERE t.id <> v_me
     AND (taskiem_link_findable(t.id)
          OR EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                      AND l.a_tenant = LEAST(v_me, t.id) AND l.b_tenant = GREATEST(v_me, t.id)))
     AND NOT EXISTS (SELECT 1 FROM storefronts s2 WHERE s2.tenant_id = t.id AND s2.status = 'suspended')
     AND NOT EXISTS (SELECT 1 FROM page_reports pr WHERE pr.tenant_id = t.id AND pr.status = 'open')
   ORDER BY ask.n;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_explore_known(uuid[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_explore_known(uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_explore_known(uuid[]) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_link_explore_known(uuid[]);
