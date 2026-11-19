-- Discover, after review (docs/spec/taskiem-for-everyone.md §10, §14, §15;
-- "As built: stage 3, Discover review fixes").
--
-- * One writer at a time per business: engine/discover takes the advisory
--   lock 'discover:'||tenant before it reads the settings and writes the
--   index (RefreshTx, SaveTx, RemoveTx, the notice), so the sweep can no
--   longer put back a listing its owner just turned off. A change to the
--   settings marks the sweep too (discover_touch on discover_settings).
-- * What may be shown is kept in the index (discover_index.hidden: off,
--   removed, suspended or reported), set by a trigger whenever the row is
--   written and at once when its settings, reports, page or business
--   change. The search filters on that column alone and uses indexes; the
--   one-business check (taskiem_discover_open) also asks the settings.
-- * Reports (the founder's rule): a report counts only when the
--   reporter's account was at least 7 days old when they made it, and not
--   when two or more of their earlier Discover reports were dismissed and
--   none was acted on. Three counting reports from different people put
--   the listing first in the moderators' queue; they hide it only when one
--   of them says scam. At most 12 Discover reports per person in two
--   hours, kept in the database. A Discover report no longer takes a
--   business out of Explore.
-- * The moderators' remove and restore and the one-off notice go through
--   definers: the business's own role writes only its own fields of
--   discover_settings.
-- * The one-off notice goes only to a listing that is shown, of a
--   business whose storefront was published when Discover shipped
--   (discover_launch, the moment of this migration), in an order that
--   moves when telling one fails.
-- * A storefront's catalogue (its items and their stock) marks the sweep
--   too, once per statement.
-- * taskiem_business_photo: a business's public photo (its profile's
--   logo, else its published page's), never one the moderators rejected.

-- +goose Up
-- When Discover shipped: businesses whose page was published by then are
-- told, once, that they are listed (§10); later ones are listed by
-- default and not told.
CREATE TABLE discover_launch (
  only_row    boolean PRIMARY KEY DEFAULT true CHECK (only_row),
  shipped_at  timestamptz NOT NULL
);
INSERT INTO discover_launch (shipped_at) VALUES (now());
ALTER TABLE discover_launch ENABLE ROW LEVEL SECURITY;
ALTER TABLE discover_launch FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON discover_launch TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON discover_launch FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (shipped_at) ON discover_launch TO taskiem_dispatch;
GRANT SELECT (created_at) ON storefront_snapshots TO taskiem_dispatch;

-- The business's own fields only; removed_* and told_at through definers.
REVOKE INSERT, UPDATE ON discover_settings FROM taskiem_app;
GRANT INSERT (tenant_id, listed, category, area, hours, lat, lng, updated_at, updated_by) ON discover_settings TO taskiem_app;
GRANT UPDATE (listed, category, area, hours, lat, lng, updated_at, updated_by) ON discover_settings TO taskiem_app;
GRANT SELECT (removed_at) ON discover_settings TO taskiem_dispatch;
GRANT INSERT (tenant_id, told_at, removed_at, removed_by, removed_note, updated_at, updated_by),
      UPDATE (told_at, removed_at, removed_by, removed_note, updated_at, updated_by) ON discover_settings TO taskiem_dispatch;
CREATE POLICY dispatch_insert ON discover_settings FOR INSERT TO taskiem_dispatch WITH CHECK (true);
CREATE POLICY dispatch_update ON discover_settings FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);

-- Reports: whether the reporter's account was 7 days old.
ALTER TABLE page_reports ADD COLUMN seasoned boolean;
GRANT SELECT (created_at) ON users TO taskiem_dispatch;
CREATE INDEX page_reports_discover_reporter ON page_reports (ip_hash, created_at) WHERE kind = 'discover';

-- The index keeps whether a listing may be shown, and its reports.
ALTER TABLE discover_index
  ADD COLUMN hidden text CHECK (hidden IN ('off', 'removed', 'suspended', 'reported')),
  ADD COLUMN reporters int NOT NULL DEFAULT 0,
  ADD COLUMN state_until timestamptz,
  ADD COLUMN notice_tried_at timestamptz,
  ADD COLUMN area_search tsvector GENERATED ALWAYS AS (to_tsvector('simple', area)) STORED,
  ADD COLUMN place point GENERATED ALWAYS AS (CASE WHEN lat IS NOT NULL AND lng IS NOT NULL THEN point(lng, lat) END) STORED;
CREATE INDEX discover_index_shown_name ON discover_index (lower(name), tenant_id) WHERE hidden IS NULL;
CREATE INDEX discover_index_shown_category ON discover_index (category, lower(name), tenant_id) WHERE hidden IS NULL;
CREATE INDEX discover_index_shown_area ON discover_index USING gin (area_search) WHERE hidden IS NULL;
CREATE INDEX discover_index_shown_place ON discover_index USING gist (place) WHERE hidden IS NULL AND place IS NOT NULL;
CREATE INDEX discover_index_state_until ON discover_index (state_until) WHERE state_until IS NOT NULL;
REVOKE INSERT, UPDATE ON discover_index FROM taskiem_app;
GRANT INSERT (tenant_id, name, tagline, category, area, address, map_link, photo, photos, hours, lat, lng, words, shop, can_chat, has_shop, has_book,
              refreshed_at) ON discover_index TO taskiem_app;
GRANT UPDATE (name, tagline, category, area, address, map_link, photo, photos, hours, lat, lng, words, shop, can_chat, has_shop, has_book,
              refreshed_at, notice_tried_at) ON discover_index TO taskiem_app;
GRANT SELECT (hidden, reporters, state_until, notice_tried_at, area_search, place), UPDATE (reporters) ON discover_index TO taskiem_dispatch;
CREATE POLICY dispatch_state ON discover_index FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);

-- A listing's reports as they count (the founder's rule, §15): open, in
-- the last 30 days, from an account at least 7 days old when it reported,
-- whose earlier Discover reports were not dismissed twice or more with
-- none acted on. How many different people, whether one says scam, and
-- when the oldest leaves the 30 days (the state is worked out again then).
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_reports(p_business uuid, OUT reporters int, OUT scam boolean, OUT until timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT count(DISTINCT r.ip_hash)::int, COALESCE(bool_or(r.reason = 'scam'), false), min(r.created_at) + interval '30 days'
    FROM page_reports r
   WHERE r.kind = 'discover' AND r.site_id = p_business AND r.status = 'open' AND r.created_at > now() - interval '30 days'
     AND r.seasoned
     AND NOT ((SELECT count(*) FROM page_reports x WHERE x.kind = 'discover' AND x.ip_hash = r.ip_hash AND x.status = 'dismissed') >= 2
              AND NOT EXISTS (SELECT 1 FROM page_reports x WHERE x.kind = 'discover' AND x.ip_hash = r.ip_hash AND x.status = 'actioned'))
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_discover_reports(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_discover_reports(uuid) FROM PUBLIC;

-- Whether an indexed listing may be shown, set on every write of its row:
-- the business active, not removed by the moderators, not turned off,
-- its page not suspended, and not hidden by reports (three counting
-- reports, one of them scam).
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_state() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r record;
BEGIN
  SELECT * INTO r FROM taskiem_discover_reports(NEW.tenant_id);
  NEW.reporters := r.reporters;
  NEW.state_until := r.until;
  NEW.hidden := CASE
    WHEN NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = NEW.tenant_id AND t.status = 'active' AND t.kind = 'business') THEN 'suspended'
    WHEN EXISTS (SELECT 1 FROM discover_settings s WHERE s.tenant_id = NEW.tenant_id AND s.removed_at IS NOT NULL) THEN 'removed'
    WHEN EXISTS (SELECT 1 FROM discover_settings s WHERE s.tenant_id = NEW.tenant_id AND s.listed = false) THEN 'off'
    WHEN EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = NEW.tenant_id AND s.status = 'suspended') THEN 'suspended'
    WHEN r.reporters >= 3 AND r.scam THEN 'reported'
  END;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_discover_state() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_discover_state() FROM PUBLIC;
CREATE TRIGGER discover_state BEFORE INSERT OR UPDATE ON discover_index
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_state();
UPDATE discover_index SET reporters = reporters;

-- A source of whether a listing may be shown changed: its row is worked
-- out again at once (the trigger above). For a report, the reporter's
-- other open Discover reports too, since a dismissal changes whether
-- they count.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_restate() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF TG_TABLE_NAME = 'page_reports' THEN
    IF NEW.kind <> 'discover' THEN
      RETURN NULL;
    END IF;
    UPDATE discover_index SET reporters = reporters
     WHERE tenant_id = NEW.site_id
        OR tenant_id IN (SELECT r.site_id FROM page_reports r WHERE r.kind = 'discover' AND r.ip_hash = NEW.ip_hash AND r.status = 'open');
  ELSIF TG_TABLE_NAME = 'tenants' THEN
    UPDATE discover_index SET reporters = reporters WHERE tenant_id = NEW.id;
  ELSE
    UPDATE discover_index SET reporters = reporters WHERE tenant_id = NEW.tenant_id;
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_discover_restate() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_discover_restate() FROM PUBLIC;
CREATE TRIGGER discover_restate AFTER INSERT OR UPDATE OF status ON page_reports
  FOR EACH ROW WHEN (NEW.kind = 'discover') EXECUTE FUNCTION taskiem_discover_restate();
CREATE TRIGGER discover_restate AFTER INSERT OR UPDATE OF listed, removed_at ON discover_settings
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_restate();
CREATE TRIGGER discover_restate AFTER UPDATE OF status ON storefronts
  FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status) EXECUTE FUNCTION taskiem_discover_restate();
CREATE TRIGGER discover_restate AFTER UPDATE OF status, kind ON tenants
  FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status OR OLD.kind IS DISTINCT FROM NEW.kind) EXECUTE FUNCTION taskiem_discover_restate();
-- A change to the settings marks the sweep as well, so a listing written
-- from an older read is written again.
CREATE TRIGGER discover_touch AFTER INSERT OR UPDATE ON discover_settings
  FOR EACH ROW EXECUTE FUNCTION taskiem_discover_touch();

-- A storefront's catalogue (SyncList, stock): once per statement, each
-- business it touched.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_touch_rows() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO discover_dirty (tenant_id)
    SELECT DISTINCT c.tenant_id FROM changed c WHERE EXISTS (SELECT 1 FROM tenants t WHERE t.id = c.tenant_id)
    ON CONFLICT (tenant_id) DO UPDATE SET at = clock_timestamp();
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_discover_touch_rows() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_discover_touch_rows() FROM PUBLIC;
GRANT SELECT (tenant_id) ON storefront_catalogue TO taskiem_dispatch;
CREATE TRIGGER discover_touch_insert AFTER INSERT ON storefront_catalogue
  REFERENCING NEW TABLE AS changed FOR EACH STATEMENT EXECUTE FUNCTION taskiem_discover_touch_rows();
CREATE TRIGGER discover_touch_update AFTER UPDATE ON storefront_catalogue
  REFERENCING NEW TABLE AS changed FOR EACH STATEMENT EXECUTE FUNCTION taskiem_discover_touch_rows();
CREATE TRIGGER discover_touch_delete AFTER DELETE ON storefront_catalogue
  REFERENCING OLD TABLE AS changed FOR EACH STATEMENT EXECUTE FUNCTION taskiem_discover_touch_rows();

-- Whether a listing may be shown now. The index's own state (above),
-- and, besides, the settings and the business asked directly: listed
-- (not turned off), not removed, an active business, its page not
-- suspended.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_discover_open(p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM discover_index d JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active' AND t.kind = 'business'
                  WHERE d.tenant_id = p_business AND d.hidden IS NULL)
     AND NOT EXISTS (SELECT 1 FROM discover_settings s WHERE s.tenant_id = p_business AND (s.listed = false OR s.removed_at IS NOT NULL))
     AND NOT EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_business AND s.status = 'suspended')
$$;
-- +goose StatementEnd

-- The search (§10), as 00461's, but over the index's own state (a plain
-- column, with indexes for no words, a category, an area and a place)
-- and with better words:
-- * a word ending in "es" after s, x, z, ch or sh also finds it without
--   ("dresses" finds "dress", "buses" "bus"), one ending in s also
--   without the s ("cakes" finds "cake"), and the word as typed always;
-- * words typed that are all left out (stop-words, single letters) find
--   nothing, not everything; "shop" and "buy" are searched.
-- With the person's place, the listings that gave one come first, the
-- nearest first (found through the place's index by straight-line
-- distance, ordered by km), then the rest as without a place.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_discover_search(p_query text, p_area text, p_category text, p_lat double precision, p_lng double precision,
  p_limit int, p_offset int)
RETURNS TABLE (business uuid, name text, category text, area text, photo jsonb, hours jsonb, can_chat boolean, km double precision)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_given boolean;
  v_terms text[];
  v_q tsquery;
  v_area tsquery;
  v_cat text := NULLIF(btrim(COALESCE(p_category, '')), '');
  v_near boolean := p_lat IS NOT NULL AND p_lng IS NOT NULL AND p_lat BETWEEN -90 AND 90 AND p_lng BETWEEN -180 AND 180;
  v_lim int := LEAST(GREATEST(COALESCE(p_limit, 20), 1), 21);
  v_off int := LEAST(GREATEST(COALESCE(p_offset, 0), 0), 200);
  v_where text := 'd.hidden IS NULL';
  v_order text;
  v_km text := 'NULL::double precision';
  v_cols text;
BEGIN
  IF COALESCE(p_query, '') ~ '(\d[\s().-]*){7,}' THEN
    RETURN;
  END IF;
  SELECT bool_or(w <> ''), array_agg(DISTINCT x.term) FILTER (WHERE x.term IS NOT NULL) INTO v_given, v_terms FROM (
    SELECT w, CASE WHEN length(w) >= 2 AND w NOT IN ('in', 'at', 'the', 'for', 'of', 'and', 'near', 'me', 'who', 'sells', 'sell', 'find', 'a', 'an', 'to', 'my')
      THEN '(' || quote_literal(w) || ':*'
           || CASE WHEN length(w) > 4 AND w ~ '(s|x|z|ch|sh)es$' THEN ' | ' || quote_literal(left(w, -2)) || ':*'
                   WHEN length(w) > 3 AND w ~ '[^s]s$' THEN ' | ' || quote_literal(left(w, -1)) || ':*'
                   ELSE '' END || ')'
      END AS term
      FROM regexp_split_to_table(lower(left(COALESCE(p_query, ''), 100)), '[^[:alnum:]]+') w
  ) x;
  IF COALESCE(v_given, false) AND v_terms IS NULL THEN
    RETURN;
  END IF;
  IF v_terms IS NOT NULL THEN
    v_q := to_tsquery('simple', array_to_string(ARRAY(SELECT t FROM unnest(v_terms) t ORDER BY t LIMIT 8), ' & '));
    v_where := v_where || ' AND d.search @@ $1';
  END IF;
  IF btrim(COALESCE(p_area, '')) <> '' THEN
    SELECT to_tsquery('simple', string_agg(quote_literal(w) || ':*', ' & ')) INTO v_area
      FROM regexp_split_to_table(lower(left(p_area, 80)), '[^[:alnum:]]+') w WHERE w <> '';
    IF v_area IS NULL THEN
      RETURN;
    END IF;
    v_where := v_where || ' AND d.area_search @@ $2';
  END IF;
  IF v_cat IS NOT NULL THEN
    v_where := v_where || ' AND d.category = $3';
  END IF;
  v_order := CASE WHEN v_q IS NOT NULL THEN 'to_tsvector(''simple'', d.name) @@ $1 DESC, ts_rank(d.search, $1) DESC, ' ELSE '' END
          || 'lower(d.name), d.tenant_id';
  IF v_near THEN
    v_km := 'round((6371 * 2 * asin(LEAST(1, sqrt(power(sin(radians(d.lat - $4) / 2), 2)'
         || ' + cos(radians($4)) * cos(radians(d.lat)) * power(sin(radians(d.lng - $5) / 2), 2)))))::numeric, 1)::double precision';
  END IF;
  v_cols := 'd.tenant_id, d.name, d.category, d.area, d.photo, d.hours, d.can_chat';
  IF v_near THEN
    RETURN QUERY EXECUTE format(
      'SELECT x.tenant_id, x.name, x.category, x.area, x.photo, x.hours, x.can_chat, x.km FROM ('
      || '(SELECT %1$s, %2$s AS km, 0 AS part, 0 AS n FROM discover_index d WHERE %3$s AND d.place IS NOT NULL'
      || ' ORDER BY d.place <-> point($5, $4) LIMIT $6 + $7)'
      || ' UNION ALL '
      || '(SELECT %1$s, NULL::double precision, 1, row_number() OVER (ORDER BY %4$s) FROM discover_index d WHERE %3$s AND d.place IS NULL'
      || ' ORDER BY %4$s LIMIT $6 + $7)) x'
      || ' ORDER BY x.part, x.km, x.n, x.tenant_id OFFSET $7 LIMIT $6', v_cols, v_km, v_where, v_order)
      USING v_q, v_area, v_cat, p_lat, p_lng, v_lim, v_off;
  ELSE
    RETURN QUERY EXECUTE format('SELECT %1$s, %2$s FROM discover_index d WHERE %3$s ORDER BY %4$s OFFSET $7 LIMIT $6', v_cols, v_km, v_where, v_order)
      USING v_q, v_area, v_cat, p_lat, p_lng, v_lim, v_off;
  END IF;
END
$$;
-- +goose StatementEnd

-- A person reports a listing (§10, §15), as 00461's, now answering why it
-- was not taken ('listing': three a day for this listing; 'many': twelve
-- Discover reports in two hours) or 'taken'; whether the reporter's
-- account was 7 days old is kept with it.
DROP FUNCTION taskiem_discover_report(uuid, uuid, text, text, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_report(p_person uuid, p_business uuid, p_reason text, p_note text, p_reporter bytea)
RETURNS text
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a report is the signed-in person''s own' USING ERRCODE = '42501';
  END IF;
  IF p_reporter IS NULL OR length(p_reporter) <> 32 OR p_reason NOT IN ('scam', 'impersonation', 'prohibited', 'offensive', 'other') THEN
    RAISE EXCEPTION 'a report has a reason of the list' USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM discover_index d JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active'
                  WHERE d.tenant_id = p_business AND (d.hidden IS NULL OR d.hidden = 'reported')) THEN
    RAISE EXCEPTION 'no such listing' USING ERRCODE = 'P0002';
  END IF;
  -- One person, one at a time: the counts below hold.
  PERFORM pg_advisory_xact_lock(hashtextextended('discover-reporter:' || encode(p_reporter, 'hex'), 0));
  IF (SELECT count(*) FROM page_reports r WHERE r.kind = 'discover' AND r.ip_hash = p_reporter AND r.created_at > now() - interval '2 hours') >= 12 THEN
    RETURN 'many';
  END IF;
  IF (SELECT count(*) FROM page_reports r WHERE r.kind = 'discover' AND r.site_id = p_business AND r.ip_hash = p_reporter
       AND r.created_at > now() - interval '1 day') >= 3 THEN
    RETURN 'listing';
  END IF;
  INSERT INTO page_reports (id, tenant_id, kind, site_id, reason, message, ip_hash, seasoned)
    VALUES (gen_random_uuid(), p_business, 'discover', p_business, p_reason, left(COALESCE(p_note, ''), 500), p_reporter,
            COALESCE((SELECT u.created_at <= now() - interval '7 days' FROM users u WHERE u.id = p_person), false));
  RETURN 'taken';
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_discover_report(uuid, uuid, text, text, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_discover_report(uuid, uuid, text, text, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_discover_report(uuid, uuid, text, text, bytea) TO taskiem_app;

-- Whether a business is due the one-off notice (§10): shown in Discover,
-- listed by default (no choice of its own), not told, and its page was
-- published when Discover shipped and is published now.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_due(p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT taskiem_discover_open(p_business)
     AND EXISTS (SELECT 1 FROM storefronts s JOIN storefront_snapshots n ON n.storefront_id = s.id
                  WHERE s.tenant_id = p_business AND s.status = 'published'
                    AND n.created_at <= (SELECT l.shipped_at FROM discover_launch l))
     AND NOT EXISTS (SELECT 1 FROM discover_settings ds WHERE ds.tenant_id = p_business AND (ds.listed IS NOT NULL OR ds.told_at IS NOT NULL))
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_discover_due(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_discover_due(uuid) FROM PUBLIC;

-- The businesses due the notice, ids only: those not tried first, then
-- the longest since they were tried, so one that keeps failing does not
-- hold up the rest.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_discover_notice_due(p_limit int) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.tenant_id FROM discover_index d
   WHERE d.hidden IS NULL AND taskiem_discover_due(d.tenant_id)
   ORDER BY d.notice_tried_at NULLS FIRST, d.tenant_id LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd

-- The notice is claimed, in the business's own scope, only while it is
-- due; true when this call claimed it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_claim_notice(p_business uuid) RETURNS boolean
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_claimed boolean;
BEGIN
  IF p_business IS NULL OR NOT p_business = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'the notice is claimed in the business''s own scope' USING ERRCODE = '42501';
  END IF;
  IF NOT taskiem_discover_due(p_business) THEN
    RETURN false;
  END IF;
  INSERT INTO discover_settings (tenant_id, told_at, updated_by) VALUES (p_business, now(), 'system:discover')
    ON CONFLICT (tenant_id) DO UPDATE SET told_at = now()
     WHERE discover_settings.told_at IS NULL AND discover_settings.listed IS NULL
    RETURNING true INTO v_claimed;
  RETURN COALESCE(v_claimed, false);
END
$$;
-- +goose StatementEnd

-- Taskiem's moderators take a listing out of Discover (a note says why)
-- or put it back: only from the operators' transaction (the platform's
-- chain in scope) widened to the business.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_moderate(p_business uuid, p_restore boolean, p_note text, p_by text) RETURNS void
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT ('ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid = ANY (taskiem_tenant_scope()) AND p_business = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'only Taskiem''s moderators take a listing out of Discover' USING ERRCODE = '42501';
  END IF;
  IF COALESCE(p_by, '') = '' THEN
    RAISE EXCEPTION 'a moderator is named' USING ERRCODE = '22023';
  END IF;
  IF p_restore THEN
    UPDATE discover_settings SET removed_at = NULL, removed_by = NULL, removed_note = NULL, updated_at = now(), updated_by = p_by
     WHERE tenant_id = p_business;
  ELSE
    IF COALESCE(btrim(p_note), '') = '' THEN
      RAISE EXCEPTION 'taking a listing out needs a reason' USING ERRCODE = '22023';
    END IF;
    INSERT INTO discover_settings (tenant_id, removed_at, removed_by, removed_note, updated_by)
      VALUES (p_business, now(), p_by, left(p_note, 500), p_by)
      ON CONFLICT (tenant_id) DO UPDATE SET removed_at = now(), removed_by = p_by, removed_note = left(p_note, 500), updated_at = now(), updated_by = p_by;
  END IF;
END
$$;
-- +goose StatementEnd

-- The listings whose report state changes with time (a counting report
-- leaving the 30 days), worked out again; how many.
-- +goose StatementBegin
CREATE FUNCTION taskiem_discover_recount(p_limit int) RETURNS int
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_n int;
BEGIN
  WITH due AS (SELECT d.tenant_id FROM discover_index d WHERE d.state_until <= now()
                ORDER BY d.state_until LIMIT LEAST(GREATEST(p_limit, 1), 500) FOR UPDATE SKIP LOCKED)
  UPDATE discover_index d SET reporters = d.reporters FROM due WHERE d.tenant_id = due.tenant_id;
  GET DIAGNOSTICS v_n = ROW_COUNT;
  RETURN v_n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_discover_notice_due(int)', 'taskiem_discover_claim_notice(uuid)',
    'taskiem_discover_moderate(uuid, boolean, text, text)', 'taskiem_discover_recount(int)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- The moderators' queue: reports on a listing with three counting
-- reports first (they look before anything else), then newest first.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_page_reports(p_status text)
RETURNS TABLE (id uuid, tenant_id uuid, tenant_name text, kind text, site_id uuid, slug text, reason text, message text, has_contact boolean,
  status text, created_at timestamptz, handled_by text, handled_at timestamptz, reports_on_site bigint, agent_code text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.id, p.tenant_id, t.name, p.kind, p.site_id, (SELECT s.slug FROM storefronts s WHERE s.id = p.site_id), p.reason, p.message,
         p.contact IS NOT NULL, p.status, p.created_at, p.handled_by, p.handled_at,
         (SELECT count(DISTINCT q.ip_hash) FROM page_reports q WHERE q.site_id = p.site_id AND q.status = 'open'),
         (SELECT a.code FROM agents a WHERE a.id = t.agent_id)
    FROM page_reports p JOIN tenants t ON t.id = p.tenant_id
   WHERE p.status = p_status OR p_status = 'all'
   ORDER BY (p.kind = 'discover' AND p.status = 'open'
             AND EXISTS (SELECT 1 FROM discover_index d WHERE d.tenant_id = p.site_id AND d.reporters >= 3)) DESC,
            p.created_at DESC LIMIT 200
$$;
-- +goose StatementEnd

-- A business's public photo: its profile's logo, else its published
-- page's logo (resolved through storefront_media, as the snapshot names
-- it), never a photo the moderators rejected; NULL for none. Runs as its
-- caller: a business's own scope, or a definer (Discover, a person's
-- Orders).
-- +goose StatementBegin
CREATE FUNCTION taskiem_business_photo(p_business uuid) RETURNS jsonb
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT COALESCE(
    (SELECT m.urls FROM business_profile bp JOIN storefront_media m ON m.tenant_id = bp.tenant_id AND m.id = bp.logo_media
      WHERE bp.tenant_id = p_business AND m.moderation <> 'rejected' AND jsonb_typeof(m.urls) = 'object' AND m.urls <> '{}'::jsonb),
    (SELECT m.urls FROM storefronts s
       JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
       JOIN storefront_media m ON m.tenant_id = s.tenant_id
        AND m.id = CASE WHEN snap.model -> 'card' ->> 'logo' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                        THEN (snap.model -> 'card' ->> 'logo')::uuid END
      WHERE s.tenant_id = p_business AND s.status = 'published' AND m.moderation <> 'rejected'
        AND jsonb_typeof(m.urls) = 'object' AND m.urls <> '{}'::jsonb))
$$;
-- +goose StatementEnd

-- Explore (00429, 00431): reports not yet looked at take a business out
-- of its sections and Featured, but not a Discover report (§10: kept
-- apart; Discover's reports hide only its listing).
-- +goose StatementBegin
DO $$
DECLARE
  f text;
  v_def text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_link_explore_open(uuid, uuid)', 'taskiem_ops_feature_add(uuid, uuid, timestamptz, timestamptz, text, text)',
    'taskiem_ops_featured()', 'taskiem_link_explore_known(uuid[])'] LOOP
    v_def := pg_get_functiondef(f::regprocedure);
    IF position('pr.status = ''open'')' IN v_def) = 0 THEN
      RAISE EXCEPTION '% does not read open reports as expected', f;
    END IF;
    EXECUTE replace(v_def, 'pr.status = ''open'')', 'pr.status = ''open'' AND pr.kind <> ''discover'')');
  END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
  f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_link_explore_open(uuid, uuid)', 'taskiem_ops_feature_add(uuid, uuid, timestamptz, timestamptz, text, text)',
    'taskiem_ops_featured()', 'taskiem_link_explore_known(uuid[])'] LOOP
    EXECUTE replace(pg_get_functiondef(f::regprocedure), 'pr.status = ''open'' AND pr.kind <> ''discover'')', 'pr.status = ''open'')');
  END LOOP;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_business_photo(uuid);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_page_reports(p_status text)
RETURNS TABLE (id uuid, tenant_id uuid, tenant_name text, kind text, site_id uuid, slug text, reason text, message text, has_contact boolean,
  status text, created_at timestamptz, handled_by text, handled_at timestamptz, reports_on_site bigint, agent_code text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.id, p.tenant_id, t.name, p.kind, p.site_id, (SELECT s.slug FROM storefronts s WHERE s.id = p.site_id), p.reason, p.message,
         p.contact IS NOT NULL, p.status, p.created_at, p.handled_by, p.handled_at,
         (SELECT count(DISTINCT q.ip_hash) FROM page_reports q WHERE q.site_id = p.site_id AND q.status = 'open'),
         (SELECT a.code FROM agents a WHERE a.id = t.agent_id)
    FROM page_reports p JOIN tenants t ON t.id = p.tenant_id
   WHERE p.status = p_status OR p_status = 'all'
   ORDER BY p.created_at DESC LIMIT 200
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_discover_recount(int);
DROP FUNCTION taskiem_discover_moderate(uuid, boolean, text, text);
DROP FUNCTION taskiem_discover_claim_notice(uuid);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_discover_notice_due(p_limit int) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.tenant_id FROM discover_index d
    JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active' AND t.kind = 'business'
   WHERE EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = d.tenant_id AND s.status = 'published')
     AND NOT EXISTS (SELECT 1 FROM discover_settings ds WHERE ds.tenant_id = d.tenant_id AND (ds.listed IS NOT NULL OR ds.told_at IS NOT NULL))
   ORDER BY d.tenant_id LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_discover_due(uuid);
DROP FUNCTION taskiem_discover_report(uuid, uuid, text, text, bytea);
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
ALTER FUNCTION taskiem_discover_report(uuid, uuid, text, text, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_discover_report(uuid, uuid, text, text, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_discover_report(uuid, uuid, text, text, bytea) TO taskiem_app;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_discover_search(p_query text, p_area text, p_category text, p_lat double precision, p_lng double precision, p_limit int, p_offset int)
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_discover_open(p_business uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM discover_index d JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active' AND t.kind = 'business'
                  WHERE d.tenant_id = p_business)
     AND NOT EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_business AND s.status = 'suspended')
     AND (SELECT count(DISTINCT r.ip_hash) FROM page_reports r
           WHERE r.kind = 'discover' AND r.site_id = p_business AND r.status = 'open' AND r.created_at > now() - interval '30 days') < 3
$$;
-- +goose StatementEnd
DROP TRIGGER discover_touch_delete ON storefront_catalogue;
DROP TRIGGER discover_touch_update ON storefront_catalogue;
DROP TRIGGER discover_touch_insert ON storefront_catalogue;
REVOKE SELECT (tenant_id) ON storefront_catalogue FROM taskiem_dispatch;
DROP FUNCTION taskiem_discover_touch_rows();
DROP TRIGGER discover_touch ON discover_settings;
DROP TRIGGER discover_restate ON tenants;
DROP TRIGGER discover_restate ON storefronts;
DROP TRIGGER discover_restate ON discover_settings;
DROP TRIGGER discover_restate ON page_reports;
DROP FUNCTION taskiem_discover_restate();
DROP TRIGGER discover_state ON discover_index;
DROP FUNCTION taskiem_discover_state();
DROP FUNCTION taskiem_discover_reports(uuid);
DROP POLICY dispatch_state ON discover_index;
REVOKE SELECT (hidden, reporters, state_until, notice_tried_at, area_search, place), UPDATE (reporters) ON discover_index FROM taskiem_dispatch;
GRANT INSERT, UPDATE ON discover_index TO taskiem_app;
ALTER TABLE discover_index DROP COLUMN place, DROP COLUMN area_search, DROP COLUMN notice_tried_at, DROP COLUMN state_until,
  DROP COLUMN reporters, DROP COLUMN hidden;
DROP INDEX page_reports_discover_reporter;
REVOKE SELECT (created_at) ON users FROM taskiem_dispatch;
ALTER TABLE page_reports DROP COLUMN seasoned;
DROP POLICY dispatch_update ON discover_settings;
DROP POLICY dispatch_insert ON discover_settings;
REVOKE INSERT, UPDATE ON discover_settings FROM taskiem_dispatch;
REVOKE SELECT (removed_at) ON discover_settings FROM taskiem_dispatch;
GRANT INSERT, UPDATE ON discover_settings TO taskiem_app;
REVOKE SELECT (created_at) ON storefront_snapshots FROM taskiem_dispatch;
DROP TABLE discover_launch;
