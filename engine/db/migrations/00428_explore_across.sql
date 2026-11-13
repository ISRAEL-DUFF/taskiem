-- Across Nigeria (docs/spec/mobile-app-explore-and-offline.md §2.1, E-S4):
-- when no business in our own area can be shown (or our area is not set),
-- Near you is replaced by findable businesses from anywhere, the fullest
-- cards first (a logo, then the most photos), then by name. The same rules
-- as every section: findable only, never ourselves or one linked with us,
-- never a suspended page.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_explore_more(p_section text, p_category text, p_limit int, p_offset int)
RETURNS TABLE (business uuid, name text, slug text, area text, tagline text, about text, has_logo boolean, photos int, state text, ver text,
               delivers boolean, vehicle text, delivery_areas text[], categories text[], links int)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_lim int := LEAST(GREATEST(p_limit, 1), 20);
  v_off int := GREATEST(p_offset, 0);
BEGIN
  IF p_section NOT IN ('trusted', 'popular', 'featured', 'category', 'across') THEN
    RETURN;
  END IF;
  IF p_section = 'featured' THEN
    v_lim := LEAST(v_lim, 6);
    v_off := 0;
  END IF;
  RETURN QUERY
  WITH cand AS (
    SELECT t.id AS biz, t.created_at AS made,
           COALESCE(NULLIF(bp.name, ''), NULLIF(snap.model->'card'->>'name', ''), t.name) AS shown_name,
           s.published_slug AS shown_slug,
           COALESCE(NULLIF(bp.area, ''), snap.model->'card'->>'area') AS shown_area,
           COALESCE(NULLIF(bp.tagline, ''), snap.model->'card'->>'tagline') AS shown_tagline,
           bp.about AS shown_about,
           COALESCE(bp.delivers, false) AS shown_delivers, bp.vehicle AS shown_vehicle, COALESCE(bp.delivery_areas, '{}') AS shown_areas,
           COALESCE(bp.categories, '{}') AS shown_cats
      FROM tenants t
      LEFT JOIN business_profile bp ON bp.tenant_id = t.id
      LEFT JOIN storefronts s ON s.tenant_id = t.id AND s.status = 'published'
      LEFT JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
     WHERE t.status = 'active' AND taskiem_link_explore_open(v_me, t.id)
       AND (p_section <> 'category' OR COALESCE(bp.categories, '{}') @> ARRAY[p_category])
  ), scored AS (
    SELECT c.*,
           CASE WHEN p_section = 'popular' THEN taskiem_link_explore_links(c.biz) ELSE 0 END AS n_links,
           -- Featured: picked (0, oldest pick first) or filled in (1).
           (SELECT min(f.created_at) FROM explore_featured f
             WHERE f.business = c.biz AND f.ended_at IS NULL AND f.starts_at <= now() AND f.until > now()) AS picked
      FROM cand c
  ), kept AS (
    SELECT sc.* FROM scored sc
     WHERE (p_section IN ('category', 'across'))
        OR (p_section = 'popular' AND sc.n_links >= 3)
        OR (p_section = 'trusted' AND taskiem_link_explore_trusted(sc.biz))
        OR (p_section = 'featured' AND (sc.picked IS NOT NULL OR taskiem_link_explore_trusted(sc.biz)))
  )
  SELECT k.biz, k.shown_name, k.shown_slug, k.shown_area, k.shown_tagline, k.shown_about,
         EXISTS (SELECT 1 FROM business_profile b2 JOIN storefront_media m ON m.id = b2.logo_media AND m.tenant_id = b2.tenant_id AND m.moderation <> 'rejected' WHERE b2.tenant_id = k.biz),
         (SELECT count(*)::int FROM taskiem_link_photo_list(k.biz)),
         COALESCE((SELECT taskiem_link_view(l, v_me) FROM business_links l WHERE l.a_tenant = LEAST(v_me, k.biz) AND l.b_tenant = GREATEST(v_me, k.biz)), 'none'),
         taskiem_link_explore_ver(k.biz),
         k.shown_delivers, k.shown_vehicle, k.shown_areas, k.shown_cats,
         CASE WHEN k.n_links >= 10 THEN (k.n_links / 10) * 10 ELSE k.n_links END
    FROM kept k
   ORDER BY CASE WHEN p_section = 'featured' THEN (k.picked IS NULL)::int END,
            CASE WHEN p_section = 'featured' THEN k.picked END,
            CASE WHEN p_section = 'featured' THEN md5(k.biz::text || current_date::text) END,
            CASE WHEN p_section = 'popular' THEN k.n_links END DESC,
            CASE WHEN p_section = 'trusted' THEN md5(k.biz::text || current_date::text) END,
            -- Across Nigeria: the fullest cards first (a logo, then photos).
            CASE WHEN p_section = 'across' THEN (EXISTS (SELECT 1 FROM business_profile b3 WHERE b3.tenant_id = k.biz AND b3.logo_media IS NOT NULL))::int END DESC,
            CASE WHEN p_section = 'across' THEN (SELECT count(*) FROM taskiem_link_photo_list(k.biz)) END DESC,
            k.shown_name, k.made DESC, k.biz
   LIMIT v_lim OFFSET v_off;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_explore_more(p_section text, p_category text, p_limit int, p_offset int)
RETURNS TABLE (business uuid, name text, slug text, area text, tagline text, about text, has_logo boolean, photos int, state text, ver text,
               delivers boolean, vehicle text, delivery_areas text[], categories text[], links int)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_lim int := LEAST(GREATEST(p_limit, 1), 20);
  v_off int := GREATEST(p_offset, 0);
BEGIN
  IF p_section NOT IN ('trusted', 'popular', 'featured', 'category') THEN
    RETURN;
  END IF;
  IF p_section = 'featured' THEN
    v_lim := LEAST(v_lim, 6);
    v_off := 0;
  END IF;
  RETURN QUERY
  WITH cand AS (
    SELECT t.id AS biz, t.created_at AS made,
           COALESCE(NULLIF(bp.name, ''), NULLIF(snap.model->'card'->>'name', ''), t.name) AS shown_name,
           s.published_slug AS shown_slug,
           COALESCE(NULLIF(bp.area, ''), snap.model->'card'->>'area') AS shown_area,
           COALESCE(NULLIF(bp.tagline, ''), snap.model->'card'->>'tagline') AS shown_tagline,
           bp.about AS shown_about,
           COALESCE(bp.delivers, false) AS shown_delivers, bp.vehicle AS shown_vehicle, COALESCE(bp.delivery_areas, '{}') AS shown_areas,
           COALESCE(bp.categories, '{}') AS shown_cats
      FROM tenants t
      LEFT JOIN business_profile bp ON bp.tenant_id = t.id
      LEFT JOIN storefronts s ON s.tenant_id = t.id AND s.status = 'published'
      LEFT JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
     WHERE t.status = 'active' AND taskiem_link_explore_open(v_me, t.id)
       AND (p_section <> 'category' OR COALESCE(bp.categories, '{}') @> ARRAY[p_category])
  ), scored AS (
    SELECT c.*,
           CASE WHEN p_section = 'popular' THEN taskiem_link_explore_links(c.biz) ELSE 0 END AS n_links,
           -- Featured: picked (0, oldest pick first) or filled in (1).
           (SELECT min(f.created_at) FROM explore_featured f
             WHERE f.business = c.biz AND f.ended_at IS NULL AND f.starts_at <= now() AND f.until > now()) AS picked
      FROM cand c
  ), kept AS (
    SELECT sc.* FROM scored sc
     WHERE (p_section = 'category')
        OR (p_section = 'popular' AND sc.n_links >= 3)
        OR (p_section = 'trusted' AND taskiem_link_explore_trusted(sc.biz))
        OR (p_section = 'featured' AND (sc.picked IS NOT NULL OR taskiem_link_explore_trusted(sc.biz)))
  )
  SELECT k.biz, k.shown_name, k.shown_slug, k.shown_area, k.shown_tagline, k.shown_about,
         EXISTS (SELECT 1 FROM business_profile b2 JOIN storefront_media m ON m.id = b2.logo_media AND m.tenant_id = b2.tenant_id AND m.moderation <> 'rejected' WHERE b2.tenant_id = k.biz),
         (SELECT count(*)::int FROM taskiem_link_photo_list(k.biz)),
         COALESCE((SELECT taskiem_link_view(l, v_me) FROM business_links l WHERE l.a_tenant = LEAST(v_me, k.biz) AND l.b_tenant = GREATEST(v_me, k.biz)), 'none'),
         taskiem_link_explore_ver(k.biz),
         k.shown_delivers, k.shown_vehicle, k.shown_areas, k.shown_cats,
         CASE WHEN k.n_links >= 10 THEN (k.n_links / 10) * 10 ELSE k.n_links END
    FROM kept k
   ORDER BY CASE WHEN p_section = 'featured' THEN (k.picked IS NULL)::int END,
            CASE WHEN p_section = 'featured' THEN k.picked END,
            CASE WHEN p_section = 'featured' THEN md5(k.biz::text || current_date::text) END,
            CASE WHEN p_section = 'popular' THEN k.n_links END DESC,
            CASE WHEN p_section = 'trusted' THEN md5(k.biz::text || current_date::text) END,
            k.shown_name, k.made DESC, k.biz
   LIMIT v_lim OFFSET v_off;
END
$$;
-- +goose StatementEnd
