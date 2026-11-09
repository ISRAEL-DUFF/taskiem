-- Riders as participants, phase 2 (docs/spec/chat-riders.md, decision
-- 0065): a rider is found in Explore as any business is. The business
-- profile says that a business delivers for others, with what and where,
-- and Explore has a Delivery section (and a search for delivery words
-- finds them, whatever their name says).
-- +goose Up
ALTER TABLE business_profile
  ADD COLUMN delivers       boolean NOT NULL DEFAULT false,
  ADD COLUMN vehicle        text CHECK (vehicle IS NULL OR length(vehicle) <= 40),
  ADD COLUMN delivery_areas text[] NOT NULL DEFAULT '{}' CHECK (cardinality(delivery_areas) <= 20);
-- Explore's definers (owned by taskiem_dispatch) read them.
GRANT SELECT (delivers, vehicle, delivery_areas) ON business_profile TO taskiem_dispatch;

-- A rider added by phone (riders.go): whether the number is another active
-- business on Taskiem (not which: that is told by its answer to the link
-- request), and our active link with that business, when there is one.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_phone_is_business(p_phone text) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id IS NOT NULL AND t.id <> taskiem_link_self()
    FROM (SELECT taskiem_link_phone_business(p_phone) AS id) t
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_phone_linked(p_phone text) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.id FROM business_links l, (SELECT taskiem_link_self() AS me, taskiem_link_phone_business(p_phone) AS them) x
   WHERE l.status = 'active' AND x.them IS NOT NULL AND x.them <> x.me
     AND l.a_tenant = LEAST(x.me, x.them) AND l.b_tenant = GREATEST(x.me, x.them)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_link_phone_is_business(text)', 'taskiem_link_phone_linked(text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

DROP FUNCTION taskiem_link_explore(text, text, int, int);

-- Businesses to browse. p_section: 'search' (what a search for p_query finds,
-- as taskiem_link_search does, at most 20, and the businesses that deliver
-- when the words ask for delivery), 'near' (findable businesses in our own
-- area, by name), 'new' (the newest findable ones) or 'delivery' (the
-- findable businesses that deliver for others, those that cover our area
-- first). All but 'search' leave out businesses we are already linked with
-- (they are in the chats). Every column is qualified: the result's own
-- names would otherwise be read as variables.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore(p_section text, p_query text, p_limit int, p_offset int)
RETURNS TABLE (business uuid, name text, slug text, area text, tagline text, about text, has_logo boolean, photos int, state text, ver text,
               delivers boolean, vehicle text, delivery_areas text[])
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_area text;
  v_lim int := LEAST(GREATEST(p_limit, 1), 20);
  v_off int := GREATEST(p_offset, 0);
  v_delivery_words boolean := lower(COALESCE(p_query, '')) ~ '(deliver|dispatch|rider|courier|logistic|okada|bike)';
BEGIN
  SELECT lower(COALESCE(NULLIF(bp.area, ''), (SELECT snap.model->'card'->>'area'
                                                FROM storefronts s JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
                                               WHERE s.tenant_id = v_me AND s.status = 'published')))
    INTO v_area
    FROM (SELECT 1) one LEFT JOIN business_profile bp ON bp.tenant_id = v_me;
  IF p_section = 'search' THEN
    RETURN QUERY
    WITH found AS (
      SELECT s.business, s.name, s.slug, s.area, s.tagline, s.state, 0 AS rank FROM taskiem_link_search(p_query, v_lim) s
      UNION ALL
      SELECT t.id, COALESCE(NULLIF(bp.name, ''), t.name), NULL::text, bp.area, bp.tagline,
             COALESCE((SELECT taskiem_link_view(l, v_me) FROM business_links l WHERE l.a_tenant = LEAST(v_me, t.id) AND l.b_tenant = GREATEST(v_me, t.id)), 'none'), 1
        FROM business_profile bp JOIN tenants t ON t.id = bp.tenant_id AND t.status = 'active'
       WHERE v_delivery_words AND bp.delivers AND t.id <> v_me AND taskiem_link_findable(t.id)
         AND NOT EXISTS (SELECT 1 FROM taskiem_link_search(p_query, v_lim) s2 WHERE s2.business = t.id)
    )
    SELECT f.business, f.name, f.slug, f.area, f.tagline, bp.about,
           EXISTS (SELECT 1 FROM business_profile b2 JOIN storefront_media m ON m.id = b2.logo_media AND m.tenant_id = b2.tenant_id AND m.moderation <> 'rejected' WHERE b2.tenant_id = f.business),
           (SELECT count(*)::int FROM taskiem_link_photo_list(f.business)),
           f.state,
           taskiem_link_explore_ver(f.business),
           COALESCE(bp.delivers, false), bp.vehicle, COALESCE(bp.delivery_areas, '{}')
      FROM found f
      LEFT JOIN business_profile bp ON bp.tenant_id = f.business
     ORDER BY f.rank, f.name
     LIMIT v_lim;
    RETURN;
  END IF;
  IF p_section NOT IN ('near', 'new', 'delivery') THEN
    RETURN;
  END IF;
  RETURN QUERY
  WITH cand AS (
    SELECT t.id AS biz, t.created_at AS made,
           COALESCE(NULLIF(bp.name, ''), NULLIF(snap.model->'card'->>'name', ''), t.name) AS shown_name,
           s.published_slug AS shown_slug,
           COALESCE(NULLIF(bp.area, ''), snap.model->'card'->>'area') AS shown_area,
           COALESCE(NULLIF(bp.tagline, ''), snap.model->'card'->>'tagline') AS shown_tagline,
           bp.about AS shown_about,
           COALESCE(bp.delivers, false) AS shown_delivers, bp.vehicle AS shown_vehicle, COALESCE(bp.delivery_areas, '{}') AS shown_areas
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
         taskiem_link_explore_ver(c.biz),
         c.shown_delivers, c.shown_vehicle, c.shown_areas
    FROM cand c
   WHERE (p_section = 'new')
      OR (p_section = 'near' AND v_area IS NOT NULL AND lower(c.shown_area) = v_area)
      OR (p_section = 'delivery' AND c.shown_delivers)
   ORDER BY CASE WHEN p_section = 'delivery' AND v_area IS NOT NULL
                  AND (lower(c.shown_area) = v_area OR EXISTS (SELECT 1 FROM unnest(c.shown_areas) a WHERE lower(a) = v_area)) THEN 0 ELSE 1 END,
            CASE WHEN p_section IN ('near', 'delivery') THEN c.shown_name END, c.made DESC, c.biz
   LIMIT v_lim OFFSET v_off;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_explore(text, text, int, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_explore(text, text, int, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_explore(text, text, int, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_link_phone_linked(text);
DROP FUNCTION taskiem_link_phone_is_business(text);
DROP FUNCTION taskiem_link_explore(text, text, int, int);
-- The function as 00401 made it.
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
ALTER FUNCTION taskiem_link_explore(text, text, int, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_explore(text, text, int, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_explore(text, text, int, int) TO taskiem_app;
ALTER TABLE business_profile DROP COLUMN delivery_areas, DROP COLUMN vehicle, DROP COLUMN delivers;
