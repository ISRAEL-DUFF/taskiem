-- Explore's scores, kept (docs/spec/mobile-app-explore-and-offline.md §2.1):
-- whether each findable business is Trusted on Taskiem, and its count of
-- links in the last 90 days, worked out every ten minutes by the server
-- (taskiem_link_explore_rescore), not for every business on every opening
-- of Explore. What a business may be shown at all (findable, not
-- suspended, not reported, not ourselves, not linked with us) is still
-- read live, so one that stops being findable leaves at once.
-- +goose Up
CREATE TABLE explore_scores (
  business   uuid PRIMARY KEY REFERENCES tenants(id),
  trusted    boolean NOT NULL,
  links      int NOT NULL,
  scored_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX explore_scores_links ON explore_scores (links DESC) WHERE links >= 3;
ALTER TABLE explore_scores ENABLE ROW LEVEL SECURITY;
ALTER TABLE explore_scores FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON explore_scores TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON explore_scores TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON explore_scores TO taskiem_dispatch;

-- +goose StatementBegin
-- How many photos a business shows (as taskiem_link_photo_list counts
-- them: its gallery, else its catalogue's), whoever asks: for the scores,
-- worked out with no business signed in.
CREATE FUNCTION taskiem_link_photo_count(p_business uuid) RETURNS int
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT CASE WHEN g.n > 0 THEN LEAST(g.n, 8) ELSE LEAST(p.n, 6) END::int
    FROM (SELECT count(*) AS n FROM business_gallery bg
            JOIN storefront_media m ON m.id = bg.media_id AND m.tenant_id = bg.tenant_id AND m.moderation <> 'rejected'
           WHERE bg.tenant_id = p_business) g,
         (SELECT count(*) AS n FROM storefronts s JOIN storefront_catalogue c ON c.storefront_id = s.id
           WHERE s.tenant_id = p_business AND s.status = 'published'
             AND jsonb_typeof(c.item->'photo') = 'object' AND COALESCE((c.item->>'available')::boolean, true)) p
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_photo_count(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_photo_count(uuid) FROM PUBLIC;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_explore_trusted(p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM tenant_verifications v WHERE v.tenant_id = p_business AND v.level IN ('phone', 'business', 'agent_onboarded'))
     AND EXISTS (SELECT 1 FROM business_profile bp JOIN storefront_media m ON m.id = bp.logo_media AND m.tenant_id = bp.tenant_id AND m.moderation <> 'rejected'
                  WHERE bp.tenant_id = p_business AND COALESCE(bp.tagline, '') <> '')
     AND taskiem_link_photo_count(p_business) >= 2
     AND (EXISTS (SELECT 1 FROM memberships mb JOIN whatsapp_bindings w ON w.user_id = mb.user_id
                   WHERE mb.tenant_id = p_business AND mb.role IN ('owner', 'admin', 'operator') AND w.last_active_at > now() - interval '30 days')
          OR EXISTS (SELECT 1 FROM memberships mb JOIN telegram_bindings tg ON tg.user_id = mb.user_id
                      WHERE mb.tenant_id = p_business AND mb.role IN ('owner', 'admin', 'operator') AND tg.last_active_at > now() - interval '30 days')
          OR EXISTS (SELECT 1 FROM link_messages lm WHERE lm.tenant_id = p_business AND lm.mine AND lm.created_at > now() - interval '30 days'))
$$;
-- +goose StatementEnd

-- Worked out again for every findable business; those no longer findable
-- go. Answers how many were scored.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore_rescore() RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE n int;
BEGIN
  INSERT INTO explore_scores (business, trusted, links, scored_at)
  SELECT t.id, taskiem_link_explore_trusted(t.id), taskiem_link_explore_links(t.id), now()
    FROM tenants t WHERE t.status = 'active' AND taskiem_link_findable(t.id)
  ON CONFLICT (business) DO UPDATE SET trusted = EXCLUDED.trusted, links = EXCLUDED.links, scored_at = EXCLUDED.scored_at;
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM explore_scores es WHERE NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = es.business AND t.status = 'active' AND taskiem_link_findable(t.id));
  RETURN n;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_explore_rescore() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_explore_rescore() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_explore_rescore() TO taskiem_app;

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
           COALESCE(bp.categories, '{}') AS shown_cats,
           COALESCE(es.trusted, false) AS is_trusted, COALESCE(es.links, 0) AS link_count
      FROM tenants t
      LEFT JOIN explore_scores es ON es.business = t.id
      LEFT JOIN business_profile bp ON bp.tenant_id = t.id
      LEFT JOIN storefronts s ON s.tenant_id = t.id AND s.status = 'published'
      LEFT JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
     WHERE t.status = 'active' AND taskiem_link_explore_open(v_me, t.id)
       AND (p_section <> 'category' OR COALESCE(bp.categories, '{}') @> ARRAY[p_category])
  ), scored AS (
    SELECT c.*,
           c.link_count AS n_links,
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
        OR (p_section = 'trusted' AND sc.is_trusted)
        OR (p_section = 'featured' AND (sc.picked IS NOT NULL OR sc.is_trusted))
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
DROP FUNCTION taskiem_link_explore_rescore();
DROP TABLE explore_scores;
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
DROP FUNCTION taskiem_link_photo_count(uuid);
