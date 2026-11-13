-- Explore, after review (docs/spec/mobile-app-explore-and-offline.md §2):
-- - a business with reports not yet looked at leaves Featured, Trusted and
--   the other sections at once, and cannot be picked (§2.2);
-- - Popular's count is rounded below ten too, and ordered by what is shown;
-- - Trusted counts the activity of the people who run a business (owner,
--   admin, operator), not of anyone who is a member of it;
-- - Across Nigeria's "fullest first" counts a logo only when it was not
--   refused;
-- - an operator's pick may start on a later date.
-- +goose Up
-- +goose StatementBegin
-- Popular's count as shown (§2.4: a count only, rounded, never under 3):
-- 3 to 4 is "3+", 5 to 9 "5+", then tens ("10+", "20+"). The section is
-- ordered by it too, so the exact count cannot be read from the order.
CREATE FUNCTION taskiem_link_explore_bucket(p_n int) RETURNS int
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE WHEN p_n >= 10 THEN (p_n / 10) * 10 WHEN p_n >= 5 THEN 5 WHEN p_n >= 3 THEN 3 ELSE 0 END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_explore_open(p_me uuid, p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p_business <> p_me AND taskiem_link_findable(p_business)
     AND NOT EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_business AND s.status = 'suspended')
     -- Reported and not yet looked at: out until an operator has (§2.2).
     AND NOT EXISTS (SELECT 1 FROM page_reports pr WHERE pr.tenant_id = p_business AND pr.status = 'open')
     AND NOT EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                      AND l.a_tenant = LEAST(p_me, p_business) AND l.b_tenant = GREATEST(p_me, p_business))
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_explore_trusted(p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM tenant_verifications v WHERE v.tenant_id = p_business AND v.level IN ('phone', 'business', 'agent_onboarded'))
     AND EXISTS (SELECT 1 FROM business_profile bp JOIN storefront_media m ON m.id = bp.logo_media AND m.tenant_id = bp.tenant_id AND m.moderation <> 'rejected'
                  WHERE bp.tenant_id = p_business AND COALESCE(bp.tagline, '') <> '')
     AND (SELECT count(*) FROM taskiem_link_photo_list(p_business)) >= 2
     AND (EXISTS (SELECT 1 FROM memberships mb JOIN whatsapp_bindings w ON w.user_id = mb.user_id
                   WHERE mb.tenant_id = p_business AND mb.role IN ('owner', 'admin', 'operator') AND w.last_active_at > now() - interval '30 days')
          OR EXISTS (SELECT 1 FROM memberships mb JOIN telegram_bindings tg ON tg.user_id = mb.user_id
                      WHERE mb.tenant_id = p_business AND mb.role IN ('owner', 'admin', 'operator') AND tg.last_active_at > now() - interval '30 days')
          OR EXISTS (SELECT 1 FROM link_messages lm WHERE lm.tenant_id = p_business AND lm.mine AND lm.created_at > now() - interval '30 days'))
$$;
-- +goose StatementEnd
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
           EXISTS (SELECT 1 FROM business_profile b3 JOIN storefront_media m3 ON m3.id = b3.logo_media AND m3.tenant_id = b3.tenant_id AND m3.moderation <> 'rejected'
                    WHERE b3.tenant_id = c.biz) AS logo_ok,
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
         taskiem_link_explore_bucket(k.n_links)
    FROM kept k
   ORDER BY CASE WHEN p_section = 'featured' THEN (k.picked IS NULL)::int END,
            CASE WHEN p_section = 'featured' THEN k.picked END,
            CASE WHEN p_section = 'featured' THEN md5(k.biz::text || current_date::text) END,
            CASE WHEN p_section = 'popular' THEN taskiem_link_explore_bucket(k.n_links) END DESC,
            CASE WHEN p_section = 'trusted' THEN md5(k.biz::text || current_date::text) END,
            -- Across Nigeria: the fullest cards first (a logo, then photos).
            CASE WHEN p_section = 'across' THEN k.logo_ok::int END DESC,
            CASE WHEN p_section = 'across' THEN (SELECT count(*) FROM taskiem_link_photo_list(k.biz)) END DESC,
            k.shown_name, k.made DESC, k.biz
   LIMIT v_lim OFFSET v_off;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_ops_feature_add(uuid, uuid, timestamptz, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_feature_add(p_id uuid, p_business uuid, p_from timestamptz, p_until timestamptz, p_note text, p_by text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = p_business AND t.status = 'active') THEN
    RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
  END IF;
  IF NOT taskiem_link_findable(p_business) THEN
    RAISE EXCEPTION 'this business is not findable in Explore' USING ERRCODE = '55000';
  END IF;
  IF EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_business AND s.status = 'suspended') THEN
    RAISE EXCEPTION 'this business''s page is suspended' USING ERRCODE = '55000';
  END IF;
  IF EXISTS (SELECT 1 FROM page_reports pr WHERE pr.tenant_id = p_business AND pr.status = 'open') THEN
    RAISE EXCEPTION 'this business has reports waiting: look at them first' USING ERRCODE = '55000';
  END IF;
  IF p_until <= now() OR p_until > now() + interval '366 days' OR p_until <= COALESCE(p_from, now())
     OR COALESCE(p_from, now()) > now() + interval '366 days' THEN
    RAISE EXCEPTION 'a pick starts and ends within a year, and ends after it starts' USING ERRCODE = '22023';
  END IF;
  INSERT INTO explore_featured (id, business, starts_at, until, note, created_by)
    VALUES (p_id, p_business, COALESCE(p_from, now()), p_until, COALESCE(p_note, ''), p_by);
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_feature_add(uuid, uuid, timestamptz, timestamptz, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_feature_add(uuid, uuid, timestamptz, timestamptz, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_feature_add(uuid, uuid, timestamptz, timestamptz, text, text) TO taskiem_app;
-- In the row now: started, not ended, findable, not suspended or reported.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_featured()
RETURNS TABLE (id uuid, business uuid, name text, starts_at timestamptz, until timestamptz, note text, created_by text, created_at timestamptz,
               ended_at timestamptz, ended_by text, shown boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT f.id, f.business, COALESCE(NULLIF(bp.name, ''), t.name), f.starts_at, f.until, f.note, f.created_by, f.created_at, f.ended_at, f.ended_by,
         f.ended_at IS NULL AND f.starts_at <= now() AND f.until > now() AND taskiem_link_findable(f.business)
           AND NOT EXISTS (SELECT 1 FROM page_reports pr WHERE pr.tenant_id = f.business AND pr.status = 'open')
           AND NOT EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = f.business AND s.status = 'suspended')
    FROM explore_featured f JOIN tenants t ON t.id = f.business LEFT JOIN business_profile bp ON bp.tenant_id = f.business
   ORDER BY (f.ended_at IS NULL AND f.until > now()) DESC, f.created_at DESC
   LIMIT 200
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_featured()
RETURNS TABLE (id uuid, business uuid, name text, starts_at timestamptz, until timestamptz, note text, created_by text, created_at timestamptz,
               ended_at timestamptz, ended_by text, shown boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT f.id, f.business, COALESCE(NULLIF(bp.name, ''), t.name), f.starts_at, f.until, f.note, f.created_by, f.created_at, f.ended_at, f.ended_by,
         f.ended_at IS NULL AND f.until > now() AND taskiem_link_findable(f.business)
           AND NOT EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = f.business AND s.status = 'suspended')
    FROM explore_featured f JOIN tenants t ON t.id = f.business LEFT JOIN business_profile bp ON bp.tenant_id = f.business
   ORDER BY (f.ended_at IS NULL AND f.until > now()) DESC, f.created_at DESC
   LIMIT 200
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_ops_feature_add(uuid, uuid, timestamptz, timestamptz, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_feature_add(p_id uuid, p_business uuid, p_until timestamptz, p_note text, p_by text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = p_business AND t.status = 'active') THEN
    RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
  END IF;
  IF NOT taskiem_link_findable(p_business) THEN
    RAISE EXCEPTION 'this business is not findable in Explore' USING ERRCODE = '55000';
  END IF;
  IF EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_business AND s.status = 'suspended') THEN
    RAISE EXCEPTION 'this business''s page is suspended' USING ERRCODE = '55000';
  END IF;
  IF p_until <= now() OR p_until > now() + interval '366 days' THEN
    RAISE EXCEPTION 'a pick ends within a year' USING ERRCODE = '22023';
  END IF;
  INSERT INTO explore_featured (id, business, until, note, created_by) VALUES (p_id, p_business, p_until, COALESCE(p_note, ''), p_by);
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_feature_add(uuid, uuid, timestamptz, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_feature_add(uuid, uuid, timestamptz, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_feature_add(uuid, uuid, timestamptz, text, text) TO taskiem_app;
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_explore_trusted(p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM tenant_verifications v WHERE v.tenant_id = p_business AND v.level IN ('phone', 'business', 'agent_onboarded'))
     AND EXISTS (SELECT 1 FROM business_profile bp JOIN storefront_media m ON m.id = bp.logo_media AND m.tenant_id = bp.tenant_id AND m.moderation <> 'rejected'
                  WHERE bp.tenant_id = p_business AND COALESCE(bp.tagline, '') <> '')
     AND (SELECT count(*) FROM taskiem_link_photo_list(p_business)) >= 2
     AND (EXISTS (SELECT 1 FROM memberships mb JOIN whatsapp_bindings w ON w.user_id = mb.user_id
                   WHERE mb.tenant_id = p_business AND w.last_active_at > now() - interval '30 days')
          OR EXISTS (SELECT 1 FROM memberships mb JOIN telegram_bindings tg ON tg.user_id = mb.user_id
                      WHERE mb.tenant_id = p_business AND tg.last_active_at > now() - interval '30 days')
          OR EXISTS (SELECT 1 FROM link_messages lm WHERE lm.tenant_id = p_business AND lm.mine AND lm.created_at > now() - interval '30 days'))
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_explore_open(p_me uuid, p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p_business <> p_me AND taskiem_link_findable(p_business)
     AND NOT EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_business AND s.status = 'suspended')
     AND NOT EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                      AND l.a_tenant = LEAST(p_me, p_business) AND l.b_tenant = GREATEST(p_me, p_business))
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_link_explore_bucket(int);
