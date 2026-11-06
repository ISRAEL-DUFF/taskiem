-- Explore (the Explore plan, PR 3): finding a business by browsing, with its
-- logo and photos. Three read-only definers, each answering only for a
-- business that chose to be found (taskiem_link_findable) or one we are
-- actively linked with, and only with what that business said in public: its
-- profile (00399), its gallery (non-rejected photos, twelve at most, eight
-- shown) and, when it has no gallery, up to six product photos from its
-- published storefront's catalogue.
-- +goose Up
GRANT SELECT (created_at) ON business_gallery TO taskiem_dispatch;

-- The photos shown for a business, in order: n counts from 0.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_photo_list(p_business uuid) RETURNS TABLE (n int, caption text, urls jsonb)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH ok AS (
    SELECT 1 AS one WHERE p_business <> taskiem_link_self() AND (
      taskiem_link_findable(p_business)
      OR EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                  AND l.a_tenant = LEAST(taskiem_link_self(), p_business) AND l.b_tenant = GREATEST(taskiem_link_self(), p_business)))
  ), gal AS (
    SELECT (row_number() OVER (ORDER BY x.pos, x.made) - 1)::int AS n, x.cap AS caption, x.urls
      FROM (SELECT g.position AS pos, g.created_at AS made, COALESCE(g.caption, '') AS cap, m.urls
              FROM ok
              JOIN business_gallery g ON g.tenant_id = p_business
              JOIN storefront_media m ON m.id = g.media_id AND m.tenant_id = g.tenant_id AND m.moderation <> 'rejected'
             ORDER BY g.position, g.created_at
             LIMIT 8) x
  ), prod AS (
    SELECT (row_number() OVER (ORDER BY y.pos) - 1)::int AS n, ''::text AS caption, y.urls
      FROM (SELECT c.position AS pos, c.item->'photo' AS urls
              FROM ok
              JOIN storefronts s ON s.tenant_id = p_business AND s.status = 'published'
              JOIN storefront_catalogue c ON c.storefront_id = s.id
             WHERE jsonb_typeof(c.item->'photo') = 'object' AND COALESCE((c.item->>'available')::boolean, true)
             ORDER BY c.position
             LIMIT 6) y
  )
  SELECT gal.n, gal.caption, gal.urls FROM gal
  UNION ALL
  SELECT prod.n, prod.caption, prod.urls FROM prod WHERE NOT EXISTS (SELECT 1 FROM gal)
$$;
-- +goose StatementEnd

-- A findable (or linked) business's logo: its stored sizes, or nothing.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore_logo(p_business uuid) RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT m.urls
    FROM business_profile bp
    JOIN storefront_media m ON m.id = bp.logo_media AND m.tenant_id = p_business AND m.moderation <> 'rejected'
   WHERE bp.tenant_id = p_business AND p_business <> taskiem_link_self()
     AND (taskiem_link_findable(p_business)
          OR EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                      AND l.a_tenant = LEAST(taskiem_link_self(), p_business) AND l.b_tenant = GREATEST(taskiem_link_self(), p_business)))
$$;
-- +goose StatementEnd

-- A short tag that changes when a business's logo or photos do: it keys the
-- pictures' cache in the browser (a photo is addressed by its place, and its
-- place changes when the owner reorders or removes one).
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore_ver(p_business uuid) RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT left(md5(COALESCE((SELECT string_agg(l.urls::text, '|' ORDER BY l.n) FROM taskiem_link_photo_list(p_business) l), '')
                  || '#' || COALESCE(taskiem_link_explore_logo(p_business)::text, '')), 8)
$$;
-- +goose StatementEnd

-- Businesses to browse. p_section: 'search' (what a search for p_query finds,
-- as taskiem_link_search does, at most 20), 'near' (findable businesses in our
-- own area, by name) or 'new' (the newest findable ones). 'near' and 'new'
-- leave out businesses we are already linked with (they are in the chats).
-- Every column is qualified: the result's own names would otherwise be read
-- as variables.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore(p_section text, p_query text, p_limit int, p_offset int)
RETURNS TABLE (business uuid, name text, slug text, area text, tagline text, about text, has_logo boolean, photos int, state text, ver text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_area text;
  v_lim int := LEAST(GREATEST(p_limit, 1), 20);
  v_off int := GREATEST(p_offset, 0);
BEGIN
  IF p_section = 'search' THEN
    RETURN QUERY
    SELECT s.business, s.name, s.slug, s.area, s.tagline, bp.about,
           EXISTS (SELECT 1 FROM business_profile b2 JOIN storefront_media m ON m.id = b2.logo_media AND m.tenant_id = b2.tenant_id AND m.moderation <> 'rejected' WHERE b2.tenant_id = s.business),
           (SELECT count(*)::int FROM taskiem_link_photo_list(s.business)),
           s.state,
           taskiem_link_explore_ver(s.business)
      FROM taskiem_link_search(p_query, v_lim) s
      LEFT JOIN business_profile bp ON bp.tenant_id = s.business;
    RETURN;
  END IF;
  IF p_section NOT IN ('near', 'new') THEN
    RETURN;
  END IF;
  SELECT lower(COALESCE(NULLIF(bp.area, ''), (SELECT snap.model->'card'->>'area'
                                                FROM storefronts s JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
                                               WHERE s.tenant_id = v_me AND s.status = 'published')))
    INTO v_area
    FROM (SELECT 1) one LEFT JOIN business_profile bp ON bp.tenant_id = v_me;
  RETURN QUERY
  WITH cand AS (
    SELECT t.id AS biz, t.created_at AS made,
           COALESCE(NULLIF(bp.name, ''), NULLIF(snap.model->'card'->>'name', ''), t.name) AS shown_name,
           s.published_slug AS shown_slug,
           COALESCE(NULLIF(bp.area, ''), snap.model->'card'->>'area') AS shown_area,
           COALESCE(NULLIF(bp.tagline, ''), snap.model->'card'->>'tagline') AS shown_tagline,
           bp.about AS shown_about
      FROM tenants t
      LEFT JOIN business_profile bp ON bp.tenant_id = t.id
      LEFT JOIN storefronts s ON s.tenant_id = t.id AND s.status = 'published'
      LEFT JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
     WHERE t.status = 'active' AND t.id <> v_me AND taskiem_link_findable(t.id)
       AND NOT EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                        AND l.a_tenant = LEAST(v_me, t.id) AND l.b_tenant = GREATEST(v_me, t.id))
  )
  SELECT c.biz, c.shown_name, c.shown_slug, c.shown_area, c.shown_tagline, c.shown_about,
         EXISTS (SELECT 1 FROM business_profile b2 JOIN storefront_media m ON m.id = b2.logo_media AND m.tenant_id = b2.tenant_id AND m.moderation <> 'rejected' WHERE b2.tenant_id = c.biz),
         (SELECT count(*)::int FROM taskiem_link_photo_list(c.biz)),
         COALESCE((SELECT taskiem_link_view(l, v_me) FROM business_links l WHERE l.a_tenant = LEAST(v_me, c.biz) AND l.b_tenant = GREATEST(v_me, c.biz)), 'none'),
         taskiem_link_explore_ver(c.biz)
    FROM cand c
   WHERE p_section = 'new' OR (v_area IS NOT NULL AND lower(c.shown_area) = v_area)
   ORDER BY CASE WHEN p_section = 'near' THEN c.shown_name END, c.made DESC, c.biz
   LIMIT v_lim OFFSET v_off;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_link_photo_list(uuid)', 'taskiem_link_explore_logo(uuid)', 'taskiem_link_explore_ver(uuid)', 'taskiem_link_explore(text, text, int, int)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
  END LOOP;
  FOREACH f IN ARRAY ARRAY['taskiem_link_photo_list(uuid)', 'taskiem_link_explore_logo(uuid)', 'taskiem_link_explore(text, text, int, int)'] LOOP
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_link_explore(text, text, int, int);
DROP FUNCTION taskiem_link_explore_ver(uuid);
DROP FUNCTION taskiem_link_explore_logo(uuid);
DROP FUNCTION taskiem_link_photo_list(uuid);
REVOKE SELECT (created_at) ON business_gallery FROM taskiem_dispatch;
