-- Discover: the public finding businesses in the Taskiem app
-- (docs/spec/taskiem-for-everyone.md §10, §14, §15, §19; stage 3).
--
-- * discover_settings: the business's switch, "Found by the public in the
--   Taskiem app", kept apart from Explore's (link_settings.findable).
--   listed NULL is the default: on for a business with a published
--   storefront, off for every other; a workflows workspace is never
--   listed whatever it says. The business may give its listing's own
--   category, area, hours and place on the map, where its profile and
--   storefront say nothing (or say something else). removed_* is set by
--   Taskiem's moderators only (the API's operator routes); told_at is when
--   the business was told, once, that it is listed by default.
-- * discover_index: the search index, one row per business shown in
--   Discover, holding only what the listing shows: name, tagline,
--   category, area, address, photos, hours, place, what it sells (words
--   only) and whether Their shop, Book and Chat are offered. Written by
--   the business's own transaction (engine/discover RefreshTx); read across
--   businesses only through the definers below. A business that is not
--   listed, or whose listing misses a category, an area, a photo or hours,
--   has no row.
-- * discover_dirty: businesses whose listing may have changed (their
--   storefront, profile, gallery, photos, chat settings, areas or tenant
--   row did), marked by triggers, so a sweep writes the index again within
--   a minute. Every business with a published storefront starts marked.
-- * page_reports take the kind 'discover': a person's report on a listing
--   reaches Taskiem's moderators beside the reports on pages. Three
--   different people with open reports in 30 days hide the listing until
--   a moderator looks.
-- * The definers: the search (taskiem_discover_search), one business's
--   listing (taskiem_discover_profile), a report (taskiem_discover_report),
--   the sweep's lists (taskiem_discover_dirty, taskiem_discover_notice_due)
--   and whether a listing may be shown (taskiem_discover_open, not granted
--   to the application: the others and the customer door call it).
--
-- Searches are not kept (§14): the search function writes nothing, and a
-- person's place, given per search, is an argument only.

-- +goose Up
CREATE TABLE discover_settings (
  tenant_id     uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  listed        boolean,
  category      text CHECK (category IS NULL OR char_length(category) <= 40),
  area          text CHECK (area IS NULL OR char_length(area) BETWEEN 1 AND 80),
  hours         jsonb CHECK (hours IS NULL OR jsonb_typeof(hours) = 'object'),
  lat           double precision CHECK (lat IS NULL OR lat BETWEEN -90 AND 90),
  lng           double precision CHECK (lng IS NULL OR lng BETWEEN -180 AND 180),
  told_at       timestamptz,
  removed_at    timestamptz,
  removed_by    text,
  removed_note  text CHECK (removed_note IS NULL OR char_length(removed_note) <= 500),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  updated_by    text NOT NULL,
  CHECK ((lat IS NULL) = (lng IS NULL))
);
ALTER TABLE discover_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE discover_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON discover_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON discover_settings TO taskiem_app;
CREATE POLICY dispatch ON discover_settings FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, listed, told_at) ON discover_settings TO taskiem_dispatch;

CREATE TABLE discover_index (
  tenant_id     uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  name          text NOT NULL,
  tagline       text NOT NULL DEFAULT '',
  category      text NOT NULL,
  area          text NOT NULL,
  address       text NOT NULL DEFAULT '',
  map_link      text NOT NULL DEFAULT '',
  photo         jsonb NOT NULL,
  photos        jsonb NOT NULL DEFAULT '[]',
  hours         jsonb NOT NULL,
  lat           double precision,
  lng           double precision,
  words         text NOT NULL CHECK (char_length(words) <= 20000),
  search        tsvector GENERATED ALWAYS AS (to_tsvector('simple', words)) STORED,
  shop          text NOT NULL DEFAULT '',
  can_chat      boolean NOT NULL,
  has_shop      boolean NOT NULL,
  has_book      boolean NOT NULL,
  refreshed_at  timestamptz NOT NULL DEFAULT now(),
  CHECK ((lat IS NULL) = (lng IS NULL))
);
CREATE INDEX discover_index_search ON discover_index USING gin (search);
CREATE INDEX discover_index_category ON discover_index (category);
ALTER TABLE discover_index ENABLE ROW LEVEL SECURITY;
ALTER TABLE discover_index FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON discover_index TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON discover_index TO taskiem_app;
CREATE POLICY dispatch ON discover_index FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, name, tagline, category, area, address, map_link, photo, photos, hours, lat, lng, search, shop, can_chat, has_shop, has_book)
  ON discover_index TO taskiem_dispatch;

CREATE TABLE discover_dirty (
  tenant_id  uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  at         timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX discover_dirty_at ON discover_dirty (at);
ALTER TABLE discover_dirty ENABLE ROW LEVEL SECURITY;
ALTER TABLE discover_dirty FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON discover_dirty TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON discover_dirty TO taskiem_app;
CREATE POLICY dispatch ON discover_dirty TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (tenant_id, at), INSERT, UPDATE (at) ON discover_dirty TO taskiem_dispatch;
INSERT INTO discover_dirty (tenant_id)
  SELECT DISTINCT s.tenant_id FROM storefronts s JOIN tenants t ON t.id = s.tenant_id AND t.kind = 'business'
   WHERE s.status = 'published';

-- A listing's source changed: the sweep writes it again. Runs as
-- taskiem_dispatch so a change made from any transaction (a moderator's,
-- a definer's) marks it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_touch() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_tenant uuid;
BEGIN
  IF TG_TABLE_NAME = 'tenants' THEN
    v_tenant := NEW.id;
  ELSIF TG_OP = 'DELETE' THEN
    v_tenant := OLD.tenant_id;
  ELSE
    v_tenant := NEW.tenant_id;
  END IF;
  -- A business being deleted takes its marks with it.
  IF v_tenant IS NOT NULL AND EXISTS (SELECT 1 FROM tenants t WHERE t.id = v_tenant) THEN
    INSERT INTO discover_dirty (tenant_id) VALUES (v_tenant)
      ON CONFLICT (tenant_id) DO UPDATE SET at = clock_timestamp();
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_discover_touch() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_discover_touch() FROM PUBLIC;
CREATE TRIGGER discover_touch AFTER INSERT OR DELETE OR UPDATE OF status, published_version, published_slug, catalogue_synced_at ON storefronts
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_touch();
CREATE TRIGGER discover_touch AFTER INSERT OR UPDATE OR DELETE ON storefront_domains
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_touch();
CREATE TRIGGER discover_touch AFTER INSERT OR UPDATE OR DELETE ON business_profile
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_touch();
CREATE TRIGGER discover_touch AFTER INSERT OR UPDATE OR DELETE ON business_gallery
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_touch();
CREATE TRIGGER discover_touch AFTER UPDATE OF moderation ON storefront_media
  FOR EACH ROW WHEN (OLD.moderation IS DISTINCT FROM NEW.moderation) EXECUTE FUNCTION taskiem_discover_touch();
CREATE TRIGGER discover_touch AFTER INSERT OR UPDATE OR DELETE ON customer_chat_link_settings
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_touch();
CREATE TRIGGER discover_touch AFTER INSERT OR UPDATE OR DELETE ON business_areas
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_touch();
CREATE TRIGGER discover_touch AFTER UPDATE OF status, kind, name ON tenants
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_touch();

ALTER TABLE page_reports DROP CONSTRAINT page_reports_kind_check,
  ADD CONSTRAINT page_reports_kind_check CHECK (kind IN ('storefront', 'form', 'discover'));
CREATE INDEX page_reports_discover ON page_reports (site_id, created_at) WHERE kind = 'discover' AND status = 'open';

-- Whether a business's listing may be shown now: it is in the index, the
-- business is an active business, its page is not suspended, and fewer
-- than three different people have open reports on the listing from the
-- last 30 days (a moderator looks first).
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_open(p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM discover_index d JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active' AND t.kind = 'business'
                  WHERE d.tenant_id = p_business)
     AND NOT EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_business AND s.status = 'suspended')
     AND (SELECT count(DISTINCT r.ip_hash) FROM page_reports r
           WHERE r.kind = 'discover' AND r.site_id = p_business AND r.status = 'open' AND r.created_at > now() - interval '30 days') < 3
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_discover_open(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_discover_open(uuid) FROM PUBLIC;

-- The search (§10): by name, what a business sells (its listing's words:
-- its name, line, category, area and the names and categories of what its
-- page offers) and area, of listings that may be shown. Each word typed
-- must match the start of a word of the listing ("cakes" finds "cake");
-- a number that could be a phone is never searched. With the person's
-- place (given for this search, kept nowhere), the nearest listings that
-- gave a place come first, with how far they are; then names that match,
-- then the best matches, then by name. Only listing columns come back.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_search(p_query text, p_area text, p_category text, p_lat double precision, p_lng double precision, p_limit int, p_offset int)
RETURNS TABLE (business uuid, name text, category text, area text, photo jsonb, hours jsonb, can_chat boolean, km double precision)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_words text[];
  v_q tsquery;
  v_area text := NULLIF(lower(btrim(left(COALESCE(p_area, ''), 80))), '');
  v_cat text := NULLIF(btrim(COALESCE(p_category, '')), '');
  v_near boolean := p_lat IS NOT NULL AND p_lng IS NOT NULL AND p_lat BETWEEN -90 AND 90 AND p_lng BETWEEN -180 AND 180;
BEGIN
  IF COALESCE(p_query, '') ~ '(\d[\s().-]*){7,}' THEN
    RETURN;
  END IF;
  SELECT array_agg(DISTINCT x.w) INTO v_words FROM (
    SELECT CASE WHEN length(w) > 3 AND w ~ '[^s]s$' THEN left(w, -1) ELSE w END AS w
      FROM regexp_split_to_table(lower(left(COALESCE(p_query, ''), 100)), '[^[:alnum:]]+') w
     WHERE length(w) >= 2 AND w NOT IN ('in', 'at', 'the', 'for', 'of', 'and', 'near', 'me', 'who', 'sells', 'sell', 'find', 'buy', 'shop', 'a', 'an', 'to', 'my')
  ) x;
  IF v_words IS NOT NULL THEN
    v_q := to_tsquery('simple', array_to_string(ARRAY(SELECT quote_literal(w) || ':*' FROM unnest(v_words) w ORDER BY w LIMIT 8), ' & '));
  END IF;
  RETURN QUERY
  SELECT d.tenant_id, d.name, d.category, d.area, d.photo, d.hours, d.can_chat, k.km
    FROM discover_index d
    CROSS JOIN LATERAL (SELECT CASE WHEN v_near AND d.lat IS NOT NULL THEN
        round((6371 * 2 * asin(LEAST(1, sqrt(power(sin(radians(d.lat - p_lat) / 2), 2)
          + cos(radians(p_lat)) * cos(radians(d.lat)) * power(sin(radians(d.lng - p_lng) / 2), 2)))))::numeric, 1)::double precision END AS km) k
   WHERE (v_q IS NULL OR d.search @@ v_q)
     AND (v_area IS NULL OR position(v_area IN lower(d.area)) > 0)
     AND (v_cat IS NULL OR d.category = v_cat)
     AND taskiem_discover_open(d.tenant_id)
   ORDER BY k.km ASC NULLS LAST,
            (v_q IS NOT NULL AND to_tsvector('simple', d.name) @@ v_q) DESC,
            CASE WHEN v_q IS NOT NULL THEN ts_rank(d.search, v_q) END DESC,
            lower(d.name), d.tenant_id
   LIMIT LEAST(GREATEST(COALESCE(p_limit, 20), 1), 21) OFFSET LEAST(GREATEST(COALESCE(p_offset, 0), 0), 200);
END
$$;
-- +goose StatementEnd

-- One business's listing (its profile in Discover), while it may be shown:
-- only its listing columns.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_profile(p_business uuid)
RETURNS TABLE (business uuid, name text, tagline text, category text, area text, address text, map_link text, photo jsonb, photos jsonb,
               hours jsonb, shop text, can_chat boolean, has_shop boolean, has_book boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.tenant_id, d.name, d.tagline, d.category, d.area, d.address, d.map_link, d.photo, d.photos, d.hours, d.shop, d.can_chat, d.has_shop, d.has_book
    FROM discover_index d WHERE d.tenant_id = p_business AND taskiem_discover_open(p_business)
$$;
-- +goose StatementEnd

-- A person reports a listing to Taskiem's moderators (§10, §15): the
-- person in scope only, a business that is in the index (shown, or hidden
-- for reports), a reason of the pages' list; three a day from one person
-- for one listing. The reporter is kept only as a keyed digest, to count
-- different people. False when it is not taken.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_report(p_person uuid, p_business uuid, p_reason text, p_note text, p_reporter bytea)
RETURNS boolean
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a report is the signed-in person''s own' USING ERRCODE = '42501';
  END IF;
  IF p_reporter IS NULL OR length(p_reporter) <> 32 OR p_reason NOT IN ('scam', 'impersonation', 'prohibited', 'offensive', 'other') THEN
    RAISE EXCEPTION 'a report has a reason of the list' USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM discover_index d JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active' WHERE d.tenant_id = p_business) THEN
    RAISE EXCEPTION 'no such listing' USING ERRCODE = 'P0002';
  END IF;
  IF (SELECT count(*) FROM page_reports r WHERE r.kind = 'discover' AND r.site_id = p_business AND r.ip_hash = p_reporter
       AND r.created_at > now() - interval '1 day') >= 3 THEN
    RETURN false;
  END IF;
  INSERT INTO page_reports (id, tenant_id, kind, site_id, reason, message, ip_hash)
    VALUES (gen_random_uuid(), p_business, 'discover', p_business, p_reason, left(COALESCE(p_note, ''), 500), p_reporter);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- The businesses whose listing may have changed, the oldest first, with
-- when they were marked (the sweep clears a mark only when it is the one
-- it read). Ids only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_dirty(p_limit int)
RETURNS TABLE (tenant_id uuid, at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.tenant_id, d.at FROM discover_dirty d ORDER BY d.at, d.tenant_id LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd

-- The businesses listed by default (a published storefront, no choice of
-- their own) that have not yet been told they are: ids only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_notice_due(p_limit int) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.tenant_id FROM discover_index d
    JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active' AND t.kind = 'business'
   WHERE EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = d.tenant_id AND s.status = 'published')
     AND NOT EXISTS (SELECT 1 FROM discover_settings ds WHERE ds.tenant_id = d.tenant_id AND (ds.listed IS NOT NULL OR ds.told_at IS NOT NULL))
   ORDER BY d.tenant_id LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_discover_search(text, text, text, double precision, double precision, int, int)',
    'taskiem_discover_profile(uuid)', 'taskiem_discover_report(uuid, uuid, text, text, bytea)', 'taskiem_discover_dirty(int)',
    'taskiem_discover_notice_due(int)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_discover_notice_due(int);
DROP FUNCTION taskiem_discover_dirty(int);
DROP FUNCTION taskiem_discover_report(uuid, uuid, text, text, bytea);
DROP FUNCTION taskiem_discover_profile(uuid);
DROP FUNCTION taskiem_discover_search(text, text, text, double precision, double precision, int, int);
DROP FUNCTION taskiem_discover_open(uuid);
DROP INDEX page_reports_discover;
DELETE FROM page_reports WHERE kind = 'discover';
ALTER TABLE page_reports DROP CONSTRAINT page_reports_kind_check,
  ADD CONSTRAINT page_reports_kind_check CHECK (kind IN ('storefront', 'form'));
DROP TRIGGER discover_touch ON tenants;
DROP TRIGGER discover_touch ON business_areas;
DROP TRIGGER discover_touch ON customer_chat_link_settings;
DROP TRIGGER discover_touch ON storefront_media;
DROP TRIGGER discover_touch ON business_gallery;
DROP TRIGGER discover_touch ON business_profile;
DROP TRIGGER discover_touch ON storefront_domains;
DROP TRIGGER discover_touch ON storefronts;
DROP FUNCTION taskiem_discover_touch();
DROP TABLE discover_dirty;
DROP TABLE discover_index;
DROP TABLE discover_settings;
