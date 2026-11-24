-- Business profile and gallery (the Explore plan, PR 2): what a business says
-- about itself to the businesses that find it, whether or not it has a
-- storefront. The profile's name, tagline, area and logo win over the
-- storefront's card; a business with neither still has its name, and a plain
-- letter shows where a logo would be. Photos reuse storefront_media (the same
-- resizing, stripping and moderation); a gallery holds twelve at most, each
-- with an optional caption. What is listed to others stays opt-in: a business
-- is found only when its findability is on (taskiem_link_findable).
-- +goose Up
CREATE TABLE business_profile (
  tenant_id   uuid PRIMARY KEY REFERENCES tenants(id),
  name        text CHECK (name IS NULL OR char_length(name) BETWEEN 1 AND 80),
  tagline     text CHECK (tagline IS NULL OR char_length(tagline) <= 140),
  area        text CHECK (area IS NULL OR char_length(area) <= 80),
  about       text CHECK (about IS NULL OR char_length(about) <= 1000),
  logo_media  uuid REFERENCES storefront_media(id),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  text NOT NULL
);
ALTER TABLE business_profile ENABLE ROW LEVEL SECURITY;
ALTER TABLE business_profile FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON business_profile TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON business_profile FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, UPDATE ON business_profile TO taskiem_app;
GRANT SELECT (tenant_id, name, tagline, area, about, logo_media) ON business_profile TO taskiem_dispatch;

CREATE TABLE business_gallery (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  media_id    uuid NOT NULL REFERENCES storefront_media(id),
  caption     text CHECK (caption IS NULL OR char_length(caption) <= 120),
  position    int NOT NULL CHECK (position >= 0),
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, media_id)
);
CREATE INDEX business_gallery_tenant ON business_gallery (tenant_id, position);
ALTER TABLE business_gallery ENABLE ROW LEVEL SECURITY;
ALTER TABLE business_gallery FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON business_gallery TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON business_gallery FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON business_gallery TO taskiem_app;
GRANT SELECT (tenant_id, media_id, caption, position) ON business_gallery TO taskiem_dispatch;

-- The search reads the profile first, then the storefront's card, then the
-- business's plain name. The same columns come back, so nothing that calls it
-- changes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_search(p_query text, p_limit int)
RETURNS TABLE (business uuid, name text, slug text, area text, tagline text, items jsonb, state text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_words text[];
  v_area text;
BEGIN
  IF p_query ~ '\d{6,}' OR p_query ~ '(\d[\s-]*){7,}' THEN
    RETURN;
  END IF;
  SELECT array_agg(DISTINCT w) INTO v_words
    FROM regexp_split_to_table(lower(left(p_query, 100)), '[\s,]+') w
   WHERE length(w) >= 2 AND w NOT IN ('in', 'at', 'the', 'for', 'of', 'and', 'near', 'who', 'sells', 'sell', 'find');
  IF v_words IS NULL THEN
    RETURN;
  END IF;
  SELECT lower(COALESCE(NULLIF(bp.area, ''), (SELECT snap.model->'card'->>'area'
                                                FROM storefronts s JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
                                               WHERE s.tenant_id = v_me AND s.status = 'published')))
    INTO v_area
    FROM (SELECT 1) one LEFT JOIN business_profile bp ON bp.tenant_id = v_me;
  RETURN QUERY
  WITH cand AS (
    SELECT t.id, COALESCE(NULLIF(bp.name, ''), NULLIF(snap.model->'card'->>'name', ''), t.name) AS name, s.published_slug AS slug,
           COALESCE(NULLIF(bp.area, ''), snap.model->'card'->>'area') AS area,
           COALESCE(NULLIF(bp.tagline, ''), snap.model->'card'->>'tagline') AS tagline, s.id AS sf,
           lower(concat_ws(' ', t.name, bp.name, bp.tagline, bp.area, bp.about, snap.model->'card'->>'name', snap.model->'card'->>'tagline', snap.model->'card'->>'area')) AS card
      FROM tenants t
      LEFT JOIN business_profile bp ON bp.tenant_id = t.id
      LEFT JOIN storefronts s ON s.tenant_id = t.id AND s.status = 'published'
      LEFT JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
     WHERE t.status = 'active' AND t.id <> v_me AND taskiem_link_findable(t.id)
  ), hit AS (
    SELECT c.*,
           (SELECT COALESCE(jsonb_agg(jsonb_build_object('name', x.item->>'name', 'price_kobo', x.item->'price_kobo') ORDER BY x.position), '[]')
              FROM (SELECT ci.item, ci.position FROM storefront_catalogue ci
                     WHERE ci.storefront_id = c.sf AND COALESCE((ci.item->>'available')::boolean, true)
                       AND EXISTS (SELECT 1 FROM unnest(v_words) w
                                    WHERE position(w IN lower(concat_ws(' ', ci.item->>'name', ci.item->>'category'))) > 0)
                     ORDER BY ci.position LIMIT 3) x) AS items
      FROM cand c
     WHERE NOT EXISTS (
       SELECT 1 FROM unnest(v_words) w
        WHERE position(w IN c.card) = 0
          AND NOT EXISTS (SELECT 1 FROM storefront_catalogue ci WHERE ci.storefront_id = c.sf
                             AND COALESCE((ci.item->>'available')::boolean, true)
                             AND position(w IN lower(concat_ws(' ', ci.item->>'name', ci.item->>'category'))) > 0))
  )
  SELECT h.id, h.name, h.slug, h.area, h.tagline, h.items,
         COALESCE((SELECT taskiem_link_view(l, v_me) FROM business_links l
                    WHERE l.a_tenant = LEAST(v_me, h.id) AND l.b_tenant = GREATEST(v_me, h.id)), 'none')
    FROM hit h
   ORDER BY EXISTS (SELECT 1 FROM business_links l WHERE l.a_tenant = LEAST(v_me, h.id) AND l.b_tenant = GREATEST(v_me, h.id) AND l.status = 'active') DESC,
            (v_area IS NOT NULL AND lower(h.area) = v_area) DESC, h.name, h.id
   LIMIT LEAST(GREATEST(p_limit, 1), 20);
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_search(p_query text, p_limit int)
RETURNS TABLE (business uuid, name text, slug text, area text, tagline text, items jsonb, state text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_words text[];
  v_area text;
BEGIN
  IF p_query ~ '\d{6,}' OR p_query ~ '(\d[\s-]*){7,}' THEN
    RETURN;
  END IF;
  SELECT array_agg(DISTINCT w) INTO v_words
    FROM regexp_split_to_table(lower(left(p_query, 100)), '[\s,]+') w
   WHERE length(w) >= 2 AND w NOT IN ('in', 'at', 'the', 'for', 'of', 'and', 'near', 'who', 'sells', 'sell', 'find');
  IF v_words IS NULL THEN
    RETURN;
  END IF;
  SELECT lower(snap.model->'card'->>'area') INTO v_area
    FROM storefronts s JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
   WHERE s.tenant_id = v_me AND s.status = 'published';
  RETURN QUERY
  WITH cand AS (
    SELECT t.id, COALESCE(NULLIF(snap.model->'card'->>'name', ''), t.name) AS name, s.published_slug AS slug,
           snap.model->'card'->>'area' AS area, snap.model->'card'->>'tagline' AS tagline, s.id AS sf,
           lower(concat_ws(' ', t.name, snap.model->'card'->>'name', snap.model->'card'->>'tagline', snap.model->'card'->>'area')) AS card
      FROM tenants t
      LEFT JOIN storefronts s ON s.tenant_id = t.id AND s.status = 'published'
      LEFT JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
     WHERE t.status = 'active' AND t.id <> v_me AND taskiem_link_findable(t.id)
  ), hit AS (
    SELECT c.*,
           (SELECT COALESCE(jsonb_agg(jsonb_build_object('name', x.item->>'name', 'price_kobo', x.item->'price_kobo') ORDER BY x.position), '[]')
              FROM (SELECT ci.item, ci.position FROM storefront_catalogue ci
                     WHERE ci.storefront_id = c.sf AND COALESCE((ci.item->>'available')::boolean, true)
                       AND EXISTS (SELECT 1 FROM unnest(v_words) w
                                    WHERE position(w IN lower(concat_ws(' ', ci.item->>'name', ci.item->>'category'))) > 0)
                     ORDER BY ci.position LIMIT 3) x) AS items
      FROM cand c
     WHERE NOT EXISTS (
       SELECT 1 FROM unnest(v_words) w
        WHERE position(w IN c.card) = 0
          AND NOT EXISTS (SELECT 1 FROM storefront_catalogue ci WHERE ci.storefront_id = c.sf
                             AND COALESCE((ci.item->>'available')::boolean, true)
                             AND position(w IN lower(concat_ws(' ', ci.item->>'name', ci.item->>'category'))) > 0))
  )
  SELECT h.id, h.name, h.slug, h.area, h.tagline, h.items,
         COALESCE((SELECT taskiem_link_view(l, v_me) FROM business_links l
                    WHERE l.a_tenant = LEAST(v_me, h.id) AND l.b_tenant = GREATEST(v_me, h.id)), 'none')
    FROM hit h
   ORDER BY EXISTS (SELECT 1 FROM business_links l WHERE l.a_tenant = LEAST(v_me, h.id) AND l.b_tenant = GREATEST(v_me, h.id) AND l.status = 'active') DESC,
            (v_area IS NOT NULL AND lower(h.area) = v_area) DESC, h.name, h.id
   LIMIT LEAST(GREATEST(p_limit, 1), 20);
END
$$;
-- +goose StatementEnd
DROP TABLE business_gallery;
DROP TABLE business_profile;
