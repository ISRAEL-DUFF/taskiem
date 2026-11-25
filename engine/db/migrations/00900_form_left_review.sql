-- +goose Up
-- Forms that sell, a review of L3 (docs/spec/forms-that-sell.md §6, §18):
--
-- 1. One record a page. A later keep from the same page replaces the
--    earlier contact whatever its kind (a phone still wins over an email),
--    so a mistyped number corrected on the page is never texted; a page
--    may name at most three contacts in all (page_names), and a keep past
--    that is refused, so one page and its keep token cannot name thirty
--    strangers. A page whose order is sent is done: the order forgets its
--    record by page as well as by contact (taskiem_form_left_sent's
--    four-argument form), and the follow-up forgets one whose page's
--    order was recorded since (taskiem_form_left_done).
-- 2. The restore link and its Stop are bound to the site's business: the
--    public host passes the tenant it serves, and a link of another
--    business is unknown there. The link's page learns which form it
--    restores, using nothing up, so a form gone from the page says so
--    without burning the link.
-- 3. The public host's list of forms that follow up is a published
--    storefront's of an active business only.
-- 4. A record whose restore link was claimed lasts a day past the claim,
--    so a buyer who opened the link just before it ran out and orders
--    after is still counted as came back.
-- 5. An order form's follow-ups default on for a One product front only
--    when the insert does not say: followup_on has no column default, and
--    the trigger sets it when it is not given.

ALTER TABLE form_left ADD COLUMN page_names smallint NOT NULL DEFAULT 1 CHECK (page_names BETWEEN 1 AND 3);

-- A page with two records (two phones typed on it before this): the
-- latest kept, the phone's over an email's.
DELETE FROM form_left l USING form_left m
 WHERE l.page_hash IS NOT NULL AND m.page_hash = l.page_hash AND m.tenant_id = l.tenant_id
   AND COALESCE(m.endpoint_id, m.action_id) = COALESCE(l.endpoint_id, l.action_id) AND m.id <> l.id AND l.followed_up_at IS NULL
   AND (m.followed_up_at IS NOT NULL OR (m.contact_kind = 'phone') > (l.contact_kind = 'phone')
        OR ((m.contact_kind = 'phone') = (l.contact_kind = 'phone') AND (m.last_keep_at, m.id) > (l.last_keep_at, l.id)));
-- Two followed up from one page: the latest keeps the page.
UPDATE form_left l SET page_hash = NULL FROM form_left m
 WHERE l.page_hash IS NOT NULL AND m.page_hash = l.page_hash AND m.tenant_id = l.tenant_id
   AND COALESCE(m.endpoint_id, m.action_id) = COALESCE(l.endpoint_id, l.action_id) AND (m.last_keep_at, m.id) > (l.last_keep_at, l.id);
CREATE UNIQUE INDEX form_left_page ON form_left (tenant_id, (COALESCE(endpoint_id, action_id)), page_hash) WHERE page_hash IS NOT NULL;

ALTER TABLE storefront_actions ALTER COLUMN followup_on DROP DEFAULT;

-- +goose StatementBegin
-- An order form added to a One product front follows up from the start
-- (§12, 6), unless the insert says otherwise; one added anywhere else
-- does not until its owner says so.
CREATE OR REPLACE FUNCTION taskiem_storefront_action_followup_default() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.followup_on IS NULL THEN
    NEW.followup_on := NEW.kind = 'order' AND EXISTS (SELECT 1 FROM storefront_fronts f WHERE f.id = NEW.front_id AND f.kind = 'one-product');
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The record, one a page: a keep from a page that has a record of another
-- contact takes its place (a typo corrected), unless that record was
-- followed up or settled already, or it is a phone and this an email, or
-- the page has named three contacts. Keeps of one page are taken one at
-- a time.
CREATE OR REPLACE FUNCTION taskiem_form_left_put(p_tenant uuid, p_endpoint uuid, p_action uuid, p_front uuid, p_site text, p_contact jsonb,
  p_hash bytea, p_kind text, p_choice jsonb, p_at timestamptz, p_page bytea, p_mask text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  got uuid;
  fresh boolean;
  scope uuid := COALESCE(p_endpoint, p_action);
  cur form_left%ROWTYPE;
  names smallint := 1;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  IF p_endpoint IS NOT NULL AND NOT EXISTS (SELECT 1 FROM form_endpoints WHERE id = p_endpoint AND tenant_id = p_tenant)
     OR p_action IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_actions a JOIN storefronts s ON s.id = a.storefront_id
                                              WHERE a.id = p_action AND s.tenant_id = p_tenant)
     OR p_front IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_actions a JOIN storefronts s ON s.id = a.storefront_id
                                             WHERE a.id = p_action AND s.tenant_id = p_tenant AND a.front_id = p_front) THEN
    RAISE EXCEPTION 'not this business''s form' USING ERRCODE = '42501';
  END IF;
  IF p_page IS NOT NULL THEN
    PERFORM pg_advisory_xact_lock(hashtextextended(encode(p_page, 'hex'), 900));
    SELECT * INTO cur FROM form_left WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = scope AND page_hash = p_page FOR UPDATE;
    IF cur.id IS NOT NULL THEN
      names := cur.page_names;
      IF cur.contact_hash <> p_hash THEN
        IF cur.followed_up_at IS NOT NULL OR cur.outcome IS NOT NULL OR (cur.contact_kind = 'phone' AND p_kind = 'email') OR cur.page_names >= 3 THEN
          RETURN NULL;
        END IF;
        names := cur.page_names + 1;
        DELETE FROM form_left WHERE id = cur.id;
      END IF;
    END IF;
  END IF;
  INSERT INTO form_left (id, tenant_id, endpoint_id, action_id, front_id, site, contact, contact_hash, contact_kind, choice, last_keep_at, expires_at, page_hash,
                         contact_mask, page_names)
  VALUES (gen_random_uuid(), p_tenant, p_endpoint, p_action, p_front, p_site, p_contact, p_hash, p_kind, COALESCE(p_choice, '{}'), p_at, p_at + interval '7 days',
          p_page, COALESCE(p_mask, ''), names)
  ON CONFLICT (tenant_id, (COALESCE(endpoint_id, action_id)), contact_hash) DO UPDATE
    SET front_id = EXCLUDED.front_id, site = COALESCE(EXCLUDED.site, form_left.site), contact = EXCLUDED.contact, contact_kind = EXCLUDED.contact_kind,
        choice = CASE WHEN form_left.followed_up_at IS NULL THEN EXCLUDED.choice ELSE form_left.choice END,
        last_keep_at = GREATEST(form_left.last_keep_at, EXCLUDED.last_keep_at),
        page_hash = CASE WHEN form_left.followed_up_at IS NULL THEN COALESCE(EXCLUDED.page_hash, form_left.page_hash) ELSE form_left.page_hash END,
        page_names = GREATEST(form_left.page_names, EXCLUDED.page_names),
        contact_mask = EXCLUDED.contact_mask,
        outcome = CASE WHEN form_left.followed_up_at IS NULL THEN NULL ELSE form_left.outcome END,
        expires_at = CASE WHEN form_left.followed_up_at IS NULL THEN GREATEST(form_left.last_keep_at, EXCLUDED.last_keep_at) + interval '7 days'
                          ELSE form_left.expires_at END
  RETURNING id, (xmax = 0) INTO got, fresh;
  IF fresh AND cur.id IS NULL THEN
    PERFORM taskiem_form_left_count(p_tenant, scope, 1, 0, 0, 0);
  END IF;
  RETURN got;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A form sent: its record found by the buyer's contact or by the page it
-- was sent from (p_page, the order's status hash; either may be NULL).
-- Within the wait the record goes; after the follow-up it came back,
-- counted once.
CREATE FUNCTION taskiem_form_left_sent(p_tenant uuid, p_scope uuid, p_hash bytea, p_page bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  DELETE FROM form_left WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = p_scope AND (contact_hash = p_hash OR page_hash = p_page)
     AND followed_up_at IS NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n > 0 THEN
    RETURN 'deleted';
  END IF;
  UPDATE form_left SET came_back_at = now()
   WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = p_scope AND (contact_hash = p_hash OR page_hash = p_page)
     AND followed_up_at IS NOT NULL AND came_back_at IS NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n > 0 THEN
    PERFORM taskiem_form_left_count(p_tenant, p_scope, 0, 0, 0, n);
    RETURN 'came_back';
  END IF;
  RETURN '';
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Before a follow-up: a record whose page's order was recorded (or is
-- waiting in the inbox) since is forgotten, not followed up.
CREATE FUNCTION taskiem_form_left_done(p_tenant uuid, p_id uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  DELETE FROM form_left l WHERE l.tenant_id = p_tenant AND l.id = p_id AND l.followed_up_at IS NULL AND l.page_hash IS NOT NULL
     AND (EXISTS (SELECT 1 FROM storefront_orders o WHERE o.tenant_id = p_tenant AND o.status_hash = l.page_hash)
          OR EXISTS (SELECT 1 FROM storefront_inbox i WHERE i.tenant_id = p_tenant AND i.status_hash = l.page_hash));
  RETURN FOUND;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Which of a storefront's actions follow up now: a published storefront's
-- of an active business only.
CREATE OR REPLACE FUNCTION taskiem_public_form_followups(p_storefront uuid)
RETURNS uuid[]
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(array_agg(a.id), '{}') FROM storefront_actions a
    JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE a.storefront_id = p_storefront AND a.enabled AND a.kind = 'order' AND a.followup_on
$$;
-- +goose StatementEnd

-- The restore link, its page and its Stop, each in the business whose
-- site serves it.
DROP FUNCTION taskiem_public_form_restore(bytea);
DROP FUNCTION taskiem_public_form_restore_ok(bytea);
DROP FUNCTION taskiem_public_form_stop(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_restore(p_hash bytea, p_tenant uuid)
RETURNS jsonb
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE form_restores r SET used_at = now()
   WHERE r.token_hash = p_hash AND r.tenant_id = p_tenant AND r.used_at IS NULL AND r.expires_at > now()
  RETURNING jsonb_build_object('endpoint_id', r.endpoint_id, 'action_id', r.action_id, 'choice', r.choice)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
-- The form a link still restores, using nothing up; NULL when it does not.
CREATE FUNCTION taskiem_public_form_restore_ok(p_hash bytea, p_tenant uuid)
RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(r.action_id, r.endpoint_id) FROM form_restores r
   WHERE r.token_hash = p_hash AND r.tenant_id = p_tenant AND r.used_at IS NULL AND r.expires_at > now()
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_stop(p_hash bytea, p_tenant uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l form_left%ROWTYPE;
BEGIN
  SELECT f.* INTO l FROM form_restores r JOIN form_left f ON f.id = r.left_id
   WHERE r.token_hash = p_hash AND r.tenant_id = p_tenant AND f.tenant_id = p_tenant AND r.expires_at > now();
  IF l.id IS NULL THEN
    RETURN false;
  END IF;
  INSERT INTO form_followup_stops (tenant_id, contact_hash) VALUES (l.tenant_id, l.contact_hash) ON CONFLICT DO NOTHING;
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Forgets what has lasted its time: records and restore links past their
-- 7 days, but a record whose link was claimed lasts a day past the claim
-- (and the link with it), so the order that follows is counted as came
-- back; keeps no api role took in a day, a contact's follow-ups after
-- their week and the counts after 90 days.
CREATE OR REPLACE FUNCTION taskiem_form_left_purge()
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
  m int;
BEGIN
  DELETE FROM form_restores WHERE expires_at <= now() AND (used_at IS NULL OR used_at <= now() - interval '24 hours');
  GET DIAGNOSTICS m = ROW_COUNT;
  DELETE FROM form_left l WHERE l.expires_at <= now()
     AND NOT EXISTS (SELECT 1 FROM form_restores r WHERE r.left_id = l.id AND r.used_at > now() - interval '24 hours');
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM form_left_inbox WHERE created_at < now() - interval '1 day';
  DELETE FROM form_followup_contacts WHERE sent_at < now() - interval '8 days';
  DELETE FROM form_left_stats WHERE day < (now() AT TIME ZONE 'UTC')::date - 90;
  RETURN n + m;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_form_left_sent(uuid, uuid, bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_done(uuid, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_restore(bytea, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_restore_ok(bytea, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_stop(bytea, uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_left_sent(uuid, uuid, bytea, bytea), taskiem_form_left_done(uuid, uuid), taskiem_public_form_restore(bytea, uuid),
  taskiem_public_form_restore_ok(bytea, uuid), taskiem_public_form_stop(bytea, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_left_sent(uuid, uuid, bytea, bytea), taskiem_form_left_done(uuid, uuid) TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_public_form_restore(bytea, uuid), taskiem_public_form_restore_ok(bytea, uuid), taskiem_public_form_stop(bytea, uuid) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_form_stop(bytea, uuid);
DROP FUNCTION taskiem_public_form_restore_ok(bytea, uuid);
DROP FUNCTION taskiem_public_form_restore(bytea, uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_restore(p_hash bytea)
RETURNS jsonb
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE form_restores r SET used_at = now()
   WHERE r.token_hash = p_hash AND r.used_at IS NULL AND r.expires_at > now()
  RETURNING jsonb_build_object('endpoint_id', r.endpoint_id, 'action_id', r.action_id, 'choice', r.choice)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_restore_ok(p_hash bytea)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM form_restores r WHERE r.token_hash = p_hash AND r.used_at IS NULL AND r.expires_at > now())
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_stop(p_hash bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l form_left%ROWTYPE;
BEGIN
  SELECT f.* INTO l FROM form_restores r JOIN form_left f ON f.id = r.left_id
   WHERE r.token_hash = p_hash AND r.expires_at > now();
  IF l.id IS NULL THEN
    RETURN false;
  END IF;
  INSERT INTO form_followup_stops (tenant_id, contact_hash) VALUES (l.tenant_id, l.contact_hash) ON CONFLICT DO NOTHING;
  RETURN true;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_form_restore(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_restore_ok(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_stop(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_restore(bytea), taskiem_public_form_restore_ok(bytea), taskiem_public_form_stop(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_restore(bytea), taskiem_public_form_restore_ok(bytea), taskiem_public_form_stop(bytea) TO taskiem_public;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_form_followups(p_storefront uuid)
RETURNS uuid[]
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(array_agg(a.id), '{}') FROM storefront_actions a
   WHERE a.storefront_id = p_storefront AND a.enabled AND a.kind = 'order' AND a.followup_on
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_form_left_purge()
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
  m int;
BEGIN
  DELETE FROM form_restores WHERE expires_at <= now();
  GET DIAGNOSTICS m = ROW_COUNT;
  DELETE FROM form_left WHERE expires_at <= now();
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM form_left_inbox WHERE created_at < now() - interval '1 day';
  DELETE FROM form_followup_contacts WHERE sent_at < now() - interval '8 days';
  DELETE FROM form_left_stats WHERE day < (now() AT TIME ZONE 'UTC')::date - 90;
  RETURN n + m;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_form_left_done(uuid, uuid);
DROP FUNCTION taskiem_form_left_sent(uuid, uuid, bytea, bytea);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_form_left_put(p_tenant uuid, p_endpoint uuid, p_action uuid, p_front uuid, p_site text, p_contact jsonb,
  p_hash bytea, p_kind text, p_choice jsonb, p_at timestamptz, p_page bytea, p_mask text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  got uuid;
  fresh boolean;
  scope uuid := COALESCE(p_endpoint, p_action);
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  IF p_endpoint IS NOT NULL AND NOT EXISTS (SELECT 1 FROM form_endpoints WHERE id = p_endpoint AND tenant_id = p_tenant)
     OR p_action IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_actions a JOIN storefronts s ON s.id = a.storefront_id
                                              WHERE a.id = p_action AND s.tenant_id = p_tenant)
     OR p_front IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_actions a JOIN storefronts s ON s.id = a.storefront_id
                                             WHERE a.id = p_action AND s.tenant_id = p_tenant AND a.front_id = p_front) THEN
    RAISE EXCEPTION 'not this business''s form' USING ERRCODE = '42501';
  END IF;
  IF p_page IS NOT NULL THEN
    IF p_kind = 'email' AND EXISTS (SELECT 1 FROM form_left WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = scope
                                       AND page_hash = p_page AND contact_kind = 'phone') THEN
      RETURN NULL;
    END IF;
    IF p_kind = 'phone' THEN
      DELETE FROM form_left WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = scope AND page_hash = p_page
        AND contact_kind = 'email' AND followed_up_at IS NULL;
    END IF;
  END IF;
  INSERT INTO form_left (id, tenant_id, endpoint_id, action_id, front_id, site, contact, contact_hash, contact_kind, choice, last_keep_at, expires_at, page_hash, contact_mask)
  VALUES (gen_random_uuid(), p_tenant, p_endpoint, p_action, p_front, p_site, p_contact, p_hash, p_kind, COALESCE(p_choice, '{}'), p_at, p_at + interval '7 days',
          p_page, COALESCE(p_mask, ''))
  ON CONFLICT (tenant_id, (COALESCE(endpoint_id, action_id)), contact_hash) DO UPDATE
    SET front_id = EXCLUDED.front_id, site = COALESCE(EXCLUDED.site, form_left.site), contact = EXCLUDED.contact, contact_kind = EXCLUDED.contact_kind,
        choice = CASE WHEN form_left.followed_up_at IS NULL THEN EXCLUDED.choice ELSE form_left.choice END,
        last_keep_at = GREATEST(form_left.last_keep_at, EXCLUDED.last_keep_at),
        page_hash = COALESCE(EXCLUDED.page_hash, form_left.page_hash), contact_mask = EXCLUDED.contact_mask,
        outcome = CASE WHEN form_left.followed_up_at IS NULL THEN NULL ELSE form_left.outcome END,
        expires_at = CASE WHEN form_left.followed_up_at IS NULL THEN GREATEST(form_left.last_keep_at, EXCLUDED.last_keep_at) + interval '7 days'
                          ELSE form_left.expires_at END
  RETURNING id, (xmax = 0) INTO got, fresh;
  IF fresh THEN
    PERFORM taskiem_form_left_count(p_tenant, scope, 1, 0, 0, 0);
  END IF;
  RETURN got;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_action_followup_default() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.kind = 'order' AND EXISTS (SELECT 1 FROM storefront_fronts f WHERE f.id = NEW.front_id AND f.kind = 'one-product') THEN
    NEW.followup_on := true;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER TABLE storefront_actions ALTER COLUMN followup_on SET DEFAULT false;

DROP INDEX form_left_page;
ALTER TABLE form_left DROP COLUMN page_names;
