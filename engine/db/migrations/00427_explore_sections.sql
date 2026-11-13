-- Explore never empty (docs/spec/mobile-app-explore-and-offline.md §2):
-- with nothing typed, the page also lists businesses Trusted on Taskiem,
-- Popular ones, Featured ones and the categories, each with its count.
--
-- - A business picks one or two categories from a fixed list (the keys
--   below; their names are the server's, engine/links/categories.go).
-- - Taskiem's operators feature a business until a date
--   (explore_featured), audited in the platform chain; the row is filled
--   to six by Trusted businesses, in an order that changes each day.
-- - Trusted: findable, verified (phone or business), a logo, a tagline,
--   at least two photos, and someone of theirs active in the last 30 days.
-- - Popular: the most active links made in the last 90 days, a count only,
--   never with whom, and never a business with fewer than three.
-- Every section keeps Explore's rules: findable businesses only, never
-- ourselves, never one we are already linked with, a suspended page never.
-- +goose Up
ALTER TABLE business_profile
  ADD COLUMN categories text[] NOT NULL DEFAULT '{}'
    CHECK (cardinality(categories) <= 2 AND categories <@ ARRAY['food', 'building', 'fashion', 'phones', 'electrical', 'beauty', 'farm', 'health',
                                                              'home', 'auto', 'logistics', 'wholesale', 'services']::text[]);
GRANT SELECT (categories) ON business_profile TO taskiem_dispatch;

CREATE TABLE explore_featured (
  id          uuid PRIMARY KEY,
  business    uuid NOT NULL REFERENCES tenants(id),
  starts_at   timestamptz NOT NULL DEFAULT now(),
  until       timestamptz NOT NULL,
  note        text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  ended_at    timestamptz,
  ended_by    text,
  CHECK (until > starts_at)
);
CREATE INDEX explore_featured_live ON explore_featured (until) WHERE ended_at IS NULL;
ALTER TABLE explore_featured ENABLE ROW LEVEL SECURITY;
ALTER TABLE explore_featured FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON explore_featured TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON explore_featured TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE ON explore_featured TO taskiem_dispatch;

-- Who was last active (Trusted): their WhatsApp or Telegram, and the
-- messages their business sent in its link chats.
GRANT SELECT (user_id, last_active_at) ON whatsapp_bindings TO taskiem_dispatch;
GRANT SELECT (user_id, last_active_at) ON telegram_bindings TO taskiem_dispatch;
CREATE POLICY dispatch_explore ON link_messages FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, mine, created_at) ON link_messages TO taskiem_dispatch;

-- A business Explore may show us at all in its new sections: findable,
-- active, not ourselves, not linked with us, its page not suspended.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore_open(p_me uuid, p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p_business <> p_me AND taskiem_link_findable(p_business)
     AND NOT EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_business AND s.status = 'suspended')
     AND NOT EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                      AND l.a_tenant = LEAST(p_me, p_business) AND l.b_tenant = GREATEST(p_me, p_business))
$$;
-- +goose StatementEnd

-- Trusted on Taskiem (§2.1).
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore_trusted(p_business uuid) RETURNS boolean
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

-- Popular (§2.1): the active links a business made in the last 90 days.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore_links(p_business uuid) RETURNS int
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT count(*)::int FROM business_links l
   WHERE l.status = 'active' AND (l.a_tenant = p_business OR l.b_tenant = p_business)
     AND COALESCE(l.answered_at, l.requested_at) > now() - interval '90 days'
$$;
-- +goose StatementEnd

-- The new sections. p_section: 'trusted', 'popular' (with its count,
-- rounded down to tens from ten, and never under three), 'featured' (the
-- operators' picks, then Trusted to fill six: no paging) or 'category'
-- (p_category, by name). Each card as taskiem_link_explore's.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore_more(p_section text, p_category text, p_limit int, p_offset int)
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

-- How many businesses we may find in each category.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_explore_categories() RETURNS TABLE (category text, n int)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c, count(*)::int
    FROM business_profile bp JOIN tenants t ON t.id = bp.tenant_id AND t.status = 'active', unnest(bp.categories) c
   WHERE taskiem_link_explore_open(taskiem_link_self(), bp.tenant_id)
   GROUP BY c
$$;
-- +goose StatementEnd

-- The categories of the businesses on cards we were already shown
-- (findable ones, or ones linked with us).
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_categories_of(p_businesses uuid[]) RETURNS TABLE (business uuid, categories text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT bp.tenant_id, bp.categories FROM business_profile bp
   WHERE bp.tenant_id = ANY (p_businesses) AND cardinality(bp.categories) > 0
     AND bp.tenant_id <> taskiem_link_self()
     AND (taskiem_link_findable(bp.tenant_id)
          OR EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                      AND l.a_tenant = LEAST(taskiem_link_self(), bp.tenant_id) AND l.b_tenant = GREATEST(taskiem_link_self(), bp.tenant_id)))
$$;
-- +goose StatementEnd

-- Operators (docs/operator-console.md): feature a business until a date,
-- end a pick, and list the picks.
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
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_feature_end(p_id uuid, p_by text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE v_biz uuid;
BEGIN
  UPDATE explore_featured SET ended_at = now(), ended_by = p_by
   WHERE id = p_id AND ended_at IS NULL RETURNING business INTO v_biz;
  IF v_biz IS NULL THEN
    RAISE EXCEPTION 'no such pick, or it has ended' USING ERRCODE = 'P0002';
  END IF;
  RETURN v_biz;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_featured()
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

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_link_explore_open(uuid, uuid)', 'taskiem_link_explore_trusted(uuid)', 'taskiem_link_explore_links(uuid)',
    'taskiem_link_explore_more(text, text, int, int)', 'taskiem_link_explore_categories()', 'taskiem_link_categories_of(uuid[])',
    'taskiem_ops_feature_add(uuid, uuid, timestamptz, text, text)', 'taskiem_ops_feature_end(uuid, text)', 'taskiem_ops_featured()'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
  END LOOP;
  -- The building blocks are the definers' own; the app calls the rest.
  FOREACH f IN ARRAY ARRAY['taskiem_link_explore_more(text, text, int, int)', 'taskiem_link_explore_categories()', 'taskiem_link_categories_of(uuid[])',
    'taskiem_ops_feature_add(uuid, uuid, timestamptz, text, text)', 'taskiem_ops_feature_end(uuid, text)', 'taskiem_ops_featured()'] LOOP
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_ops_featured();
DROP FUNCTION taskiem_ops_feature_end(uuid, text);
DROP FUNCTION taskiem_ops_feature_add(uuid, uuid, timestamptz, text, text);
DROP FUNCTION taskiem_link_categories_of(uuid[]);
DROP FUNCTION taskiem_link_explore_categories();
DROP FUNCTION taskiem_link_explore_more(text, text, int, int);
DROP FUNCTION taskiem_link_explore_links(uuid);
DROP FUNCTION taskiem_link_explore_trusted(uuid);
DROP FUNCTION taskiem_link_explore_open(uuid, uuid);
REVOKE SELECT (tenant_id, mine, created_at) ON link_messages FROM taskiem_dispatch;
DROP POLICY dispatch_explore ON link_messages;
REVOKE SELECT (user_id, last_active_at) ON telegram_bindings FROM taskiem_dispatch;
REVOKE SELECT (user_id, last_active_at) ON whatsapp_bindings FROM taskiem_dispatch;
DROP TABLE explore_featured;
ALTER TABLE business_profile DROP COLUMN categories;
