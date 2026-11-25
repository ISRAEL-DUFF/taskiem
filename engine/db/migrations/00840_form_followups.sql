-- +goose Up
-- Forms that sell L3 (docs/spec/forms-that-sell.md §6, §10): following up
-- a form left half-filled. An order form follows up only when its owner
-- turns it on (followup_on; on by default for an action of a One product
-- front), after its wait (followup_wait, 10 minutes to 24 hours). The
-- public host's keep is refused for a form that does not follow up and
-- for a page whose form was sent already (its order's status hash). The
-- api role takes records past their wait, counts them as left, and sends
-- one follow-up within the caps: one per contact a day and three a week
-- across every form and business (form_followup_contacts, keyed hashes
-- only), 50 a day per form and 100 per business, the plan's month
-- (form_followups), and a hold on a form whose keeps jump. A contact who
-- stopped them (form_followup_stops) gets none from that business. The
-- counts live in form_left_stats, kept apart from the records so Came back
-- outlasts them. Erasing a person erases their records.

ALTER TABLE storefront_actions
  ADD COLUMN followup_on boolean NOT NULL DEFAULT false,
  ADD COLUMN followup_wait int NOT NULL DEFAULT 30 CHECK (followup_wait BETWEEN 10 AND 1440),
  ADD COLUMN followup_held_at timestamptz,
  ADD COLUMN followup_released_at timestamptz;
GRANT SELECT (tenant_id, followup_on, followup_wait, followup_held_at) ON storefront_actions TO taskiem_dispatch;
GRANT SELECT (tenant_id) ON storefront_fronts TO taskiem_dispatch;

ALTER TABLE tenant_limits ADD COLUMN form_followups bigint CHECK (form_followups >= 0);

-- The record's own state: the page it came from (two contacts typed on one
-- page make one record, the phone's), the contact masked for the owner's
-- list, when it was counted as left, what became of it and by which
-- channel it was followed up, and the api role's claim on it.
ALTER TABLE form_left
  ADD COLUMN page_hash bytea CHECK (length(page_hash) = 32),
  ADD COLUMN contact_mask text NOT NULL DEFAULT '' CHECK (length(contact_mask) <= 80),
  ADD COLUMN left_at timestamptz,
  ADD COLUMN outcome text CHECK (outcome IN ('followed', 'capped', 'stopped', 'limit', 'off', 'stale', 'failed')),
  ADD COLUMN channel text CHECK (channel IN ('sms', 'email', 'whatsapp')),
  ADD COLUMN claimed_until timestamptz;
CREATE INDEX form_left_due ON form_left (last_keep_at) WHERE outcome IS NULL AND followed_up_at IS NULL;

-- A restore link's record, so the link's page can stop the follow-ups.
ALTER TABLE form_restores ADD COLUMN left_id uuid REFERENCES form_left(id) ON DELETE SET NULL;

-- The counts, per form (endpoint or action) and UTC day.
CREATE TABLE form_left_stats (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  scope_id   uuid NOT NULL,
  day        date NOT NULL,
  kept       int NOT NULL DEFAULT 0,
  left_n     int NOT NULL DEFAULT 0,
  followed   int NOT NULL DEFAULT 0,
  came_back  int NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, scope_id, day)
);

-- Follow-ups sent to a contact, across businesses: its keyed hash only.
CREATE TABLE form_followup_contacts (
  contact_hash bytea NOT NULL CHECK (length(contact_hash) = 32),
  sent_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX form_followup_contacts_hash ON form_followup_contacts (contact_hash, sent_at);

-- A contact who stopped a business's follow-ups.
CREATE TABLE form_followup_stops (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  contact_hash bytea NOT NULL CHECK (length(contact_hash) = 32),
  stopped_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, contact_hash)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['form_left_stats', 'form_followup_contacts', 'form_followup_stops'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY no_direct_access ON %I TO taskiem_app USING (false) WITH CHECK (false)', t);
    EXECUTE format('CREATE POLICY dispatch ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', t);
    EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_dispatch', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- An order form added to a One product front follows up from the start
-- (§12, 6); one added anywhere else does not until its owner says so.
CREATE FUNCTION taskiem_storefront_action_followup_default() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.kind = 'order' AND EXISTS (SELECT 1 FROM storefront_fronts f WHERE f.id = NEW.front_id AND f.kind = 'one-product') THEN
    NEW.followup_on := true;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_storefront_action_followup_default() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefront_action_followup_default() FROM PUBLIC;
CREATE TRIGGER storefront_actions_followup_default BEFORE INSERT ON storefront_actions
  FOR EACH ROW EXECUTE FUNCTION taskiem_storefront_action_followup_default();

-- +goose StatementBegin
-- One day's count of a form, added to.
CREATE FUNCTION taskiem_form_left_count(p_tenant uuid, p_scope uuid, p_kept int, p_left int, p_followed int, p_back int)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO form_left_stats (tenant_id, scope_id, day, kept, left_n, followed, came_back)
  VALUES (p_tenant, p_scope, (now() AT TIME ZONE 'UTC')::date, p_kept, p_left, p_followed, p_back)
  ON CONFLICT (tenant_id, scope_id, day) DO UPDATE SET kept = form_left_stats.kept + p_kept, left_n = form_left_stats.left_n + p_left,
    followed = form_left_stats.followed + p_followed, came_back = form_left_stats.came_back + p_back
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_left_count(uuid, uuid, int, int, int, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_left_count(uuid, uuid, int, int, int, int) FROM PUBLIC;

-- The public host's keep, now only for an order form whose follow-up is on
-- (the review of L0: any live form took keeps), and never for a page whose
-- form was sent already: p_sent is the status hash its order has or would
-- have. A hosted form that sells is drawn at its action, so a keep is
-- always an action's.
DROP FUNCTION taskiem_public_form_keep(uuid, uuid, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_keep(p_action uuid, p_payload bytea, p_sent bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  tenant uuid;
BEGIN
  IF p_action IS NULL OR p_sent IS NULL OR length(p_sent) <> 32 THEN
    RETURN false;
  END IF;
  SELECT a.tenant_id INTO tenant FROM storefront_actions a JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
    JOIN tenants t ON t.id = a.tenant_id AND t.status = 'active'
   WHERE a.id = p_action AND a.enabled AND a.kind = 'order' AND a.followup_on;
  IF tenant IS NULL THEN
    RETURN false;
  END IF;
  IF EXISTS (SELECT 1 FROM storefront_inbox i WHERE i.status_hash = p_sent)
     OR EXISTS (SELECT 1 FROM storefront_orders o WHERE o.status_hash = p_sent) THEN
    RETURN false;
  END IF;
  IF (SELECT count(*) FROM (SELECT 1 FROM form_left_inbox WHERE tenant_id = tenant LIMIT 2001) x) > 2000 THEN
    RETURN false;
  END IF;
  INSERT INTO form_left_inbox (id, tenant_id, action_id, payload) VALUES (gen_random_uuid(), tenant, p_action, p_payload);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Which of a storefront's actions follow up now: the public host draws
-- the consent line on their forms (and sends keeps from them only).
CREATE FUNCTION taskiem_public_form_followups(p_storefront uuid)
RETURNS uuid[]
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(array_agg(a.id), '{}') FROM storefront_actions a
   WHERE a.storefront_id = p_storefront AND a.enabled AND a.kind = 'order' AND a.followup_on
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The link's page can stop the follow-ups: every follow-up to that
-- contact from that business, for as long as the link lasts. Used or
-- not; the contact is never read.
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

-- +goose StatementBegin
-- The restore link's page, before it is used: whether it still restores.
CREATE FUNCTION taskiem_public_form_restore_ok(p_hash bytea)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM form_restores r WHERE r.token_hash = p_hash AND r.used_at IS NULL AND r.expires_at > now())
$$;
-- +goose StatementEnd

-- The record, with the page it came from and the contact masked. A phone
-- typed on a page takes the place of an email typed on the same page, and
-- an email after a phone on it is not kept, so one message is sent. A
-- later keep on a record not yet followed up makes it wait again.
DROP FUNCTION taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz);
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_left_put(p_tenant uuid, p_endpoint uuid, p_action uuid, p_front uuid, p_site text, p_contact jsonb,
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
     OR p_action IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_actions WHERE id = p_action AND tenant_id = p_tenant)
     OR p_front IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_fronts WHERE id = p_front AND tenant_id = p_tenant) THEN
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
-- The records past their wait, across businesses, claimed for a few
-- minutes: what the api role follows up. A form's own wait counts from
-- the last keep; a form no longer following up is in the list too, to be
-- counted as left and not followed up.
CREATE FUNCTION taskiem_form_left_due(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, action_id uuid, front_id uuid, site text, contact jsonb, contact_kind text, contact_hash bytea,
  choice jsonb, last_keep_at timestamptz, followup_on boolean, held boolean)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE form_left l SET claimed_until = now() + interval '5 minutes'
    FROM storefront_actions a
   WHERE a.id = l.action_id AND l.id IN (
     SELECT x.id FROM form_left x JOIN storefront_actions y ON y.id = x.action_id
      WHERE x.outcome IS NULL AND x.followed_up_at IS NULL AND (x.claimed_until IS NULL OR x.claimed_until < now())
        AND x.last_keep_at + make_interval(mins => y.followup_wait) <= now()
      ORDER BY x.last_keep_at LIMIT LEAST(GREATEST(p_limit, 1), 200) FOR UPDATE OF x SKIP LOCKED)
  RETURNING l.id, l.tenant_id, l.action_id, l.front_id, l.site, l.contact, l.contact_kind, l.contact_hash, l.choice, l.last_keep_at,
            a.followup_on, a.followup_held_at IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A record past its wait: counted as left (once), and what became of it.
-- A NULL outcome is a record held: it is looked at again in a minute.
CREATE FUNCTION taskiem_form_left_mark(p_tenant uuid, p_id uuid, p_outcome text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  was timestamptz;
  scope uuid;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  SELECT left_at, COALESCE(endpoint_id, action_id) INTO was, scope FROM form_left WHERE tenant_id = p_tenant AND id = p_id FOR UPDATE;
  IF scope IS NULL THEN
    RETURN;
  END IF;
  UPDATE form_left SET left_at = COALESCE(left_at, now()), outcome = p_outcome,
         claimed_until = CASE WHEN p_outcome IS NULL THEN now() + interval '1 minute' ELSE NULL END
   WHERE tenant_id = p_tenant AND id = p_id AND followed_up_at IS NULL;
  IF was IS NULL THEN
    PERFORM taskiem_form_left_count(p_tenant, scope, 0, 1, 0, 0);
  END IF;
END
$$;
-- +goose StatementEnd

-- The follow-up sent: counted, with its channel, and its restore link
-- names the record so the link's page can stop the follow-ups.
DROP FUNCTION taskiem_form_left_followed(uuid, uuid, bytea, timestamptz);
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_left_followed(p_tenant uuid, p_id uuid, p_token_hash bytea, p_at timestamptz, p_channel text)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l form_left%ROWTYPE;
  was timestamptz;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  SELECT left_at INTO was FROM form_left WHERE tenant_id = p_tenant AND id = p_id;
  UPDATE form_left SET followed_up_at = p_at, expires_at = p_at + interval '7 days', outcome = 'followed', channel = p_channel,
         left_at = COALESCE(left_at, p_at), claimed_until = NULL
   WHERE tenant_id = p_tenant AND id = p_id AND followed_up_at IS NULL
  RETURNING * INTO l;
  IF l.id IS NULL THEN
    RETURN false;
  END IF;
  INSERT INTO form_restores (token_hash, tenant_id, endpoint_id, action_id, choice, expires_at, left_id)
  VALUES (p_token_hash, p_tenant, l.endpoint_id, l.action_id, l.choice, p_at + interval '7 days', l.id);
  PERFORM taskiem_form_left_count(p_tenant, COALESCE(l.endpoint_id, l.action_id), 0, CASE WHEN was IS NULL THEN 1 ELSE 0 END, 1, 0);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- A submission after the follow-up: came back, counted once.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_form_left_sent(p_tenant uuid, p_scope uuid, p_hash bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  DELETE FROM form_left WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = p_scope AND contact_hash = p_hash AND followed_up_at IS NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n > 0 THEN
    RETURN 'deleted';
  END IF;
  UPDATE form_left SET came_back_at = now()
   WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = p_scope AND contact_hash = p_hash AND followed_up_at IS NOT NULL AND came_back_at IS NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n > 0 THEN
    PERFORM taskiem_form_left_count(p_tenant, p_scope, 0, 0, 0, 1);
    RETURN 'came_back';
  END IF;
  RETURN '';
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- One follow-up to a contact across every form and business: at most one
-- in 24 hours and three in 7 days. Takes one when it may.
CREATE FUNCTION taskiem_form_followup_take(p_hash bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_hash IS NULL OR length(p_hash) <> 32 THEN
    RETURN false;
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(encode(p_hash, 'hex'), 840));
  IF EXISTS (SELECT 1 FROM form_followup_contacts WHERE contact_hash = p_hash AND sent_at > now() - interval '24 hours')
     OR (SELECT count(*) FROM form_followup_contacts WHERE contact_hash = p_hash AND sent_at > now() - interval '7 days') >= 3 THEN
    RETURN false;
  END IF;
  INSERT INTO form_followup_contacts (contact_hash) VALUES (p_hash);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- What the caps read for one record: follow-ups in the last 24 hours on
-- the form and in the business, this UTC month in the business; records
-- kept on the form today and its orders a day over four weeks (the hold);
-- and whether the contact stopped this business's follow-ups.
CREATE FUNCTION taskiem_form_left_caps(p_tenant uuid, p_scope uuid, p_hash bytea)
RETURNS TABLE (form_day int, business_day int, business_month int, kept_today int, orders_daily numeric, stopped boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT
    (SELECT count(*)::int FROM form_left WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = p_scope AND followed_up_at > now() - interval '24 hours'),
    (SELECT count(*)::int FROM form_left WHERE tenant_id = p_tenant AND followed_up_at > now() - interval '24 hours'),
    (SELECT COALESCE(sum(followed), 0)::int FROM form_left_stats WHERE tenant_id = p_tenant AND day >= date_trunc('month', now() AT TIME ZONE 'UTC')::date),
    (SELECT COALESCE(sum(kept), 0)::int FROM form_left_stats WHERE tenant_id = p_tenant AND scope_id = p_scope AND day = (now() AT TIME ZONE 'UTC')::date),
    (SELECT count(*)::numeric / 28 FROM storefront_orders o WHERE o.tenant_id = p_tenant AND o.action_id = p_scope AND o.created_at > now() - interval '28 days'),
    EXISTS (SELECT 1 FROM form_followup_stops s WHERE s.tenant_id = p_tenant AND s.contact_hash = p_hash);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A contact stops a business's follow-ups (in the caller's tenant).
CREATE FUNCTION taskiem_form_followup_stop(p_tenant uuid, p_hash bytea)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  INSERT INTO form_followup_stops (tenant_id, contact_hash) VALUES (p_tenant, p_hash) ON CONFLICT DO NOTHING;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The owner's counts for a form since a day: kept, left, followed up and
-- came back.
CREATE FUNCTION taskiem_form_left_summary(p_tenant uuid, p_scope uuid, p_since date)
RETURNS TABLE (kept int, left_n int, followed int, came_back int)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT COALESCE(sum(s.kept), 0)::int, COALESCE(sum(s.left_n), 0)::int, COALESCE(sum(s.followed), 0)::int, COALESCE(sum(s.came_back), 0)::int
    FROM form_left_stats s WHERE s.tenant_id = p_tenant AND s.scope_id = p_scope AND s.day >= p_since;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The owner's list for a form: the records that left, newest first, with
-- the contact masked and never opened.
CREATE FUNCTION taskiem_form_left_list(p_tenant uuid, p_scope uuid)
RETURNS TABLE (id uuid, contact_kind text, contact_mask text, choice jsonb, last_keep_at timestamptz, left_at timestamptz,
  followed_up_at timestamptz, came_back_at timestamptz, channel text, outcome text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT l.id, l.contact_kind, l.contact_mask, l.choice, l.last_keep_at, l.left_at, l.followed_up_at, l.came_back_at, l.channel, l.outcome
    FROM form_left l WHERE l.tenant_id = p_tenant AND COALESCE(l.endpoint_id, l.action_id) = p_scope AND l.left_at IS NOT NULL
   ORDER BY l.last_keep_at DESC LIMIT 100;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Today's counts of each of a business's forms with any: the chat alert.
CREATE FUNCTION taskiem_form_left_today(p_tenant uuid)
RETURNS TABLE (scope_id uuid, left_n int, followed int)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT s.scope_id, s.left_n, s.followed FROM form_left_stats s
   WHERE s.tenant_id = p_tenant AND s.day = (now() AT TIME ZONE 'UTC')::date AND s.left_n > 0;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Erasing a person erases their records (sealed under their subject).
CREATE FUNCTION taskiem_form_left_erased() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM form_left WHERE tenant_id = NEW.tenant_id AND contact->>'subject' = NEW.subject_id;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_left_erased() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_left_erased() FROM PUBLIC;
CREATE TRIGGER form_left_erased AFTER UPDATE OF shredded_at ON subject_keys
  FOR EACH ROW WHEN (OLD.shredded_at IS NULL AND NEW.shredded_at IS NOT NULL) EXECUTE FUNCTION taskiem_form_left_erased();

-- +goose StatementBegin
-- Forgets what has lasted its time: records and restore links past their
-- 7 days, keeps no api role took in a day, a contact's follow-ups after
-- their week and the counts after 90 days.
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

ALTER FUNCTION taskiem_public_form_keep(uuid, bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_followups(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_stop(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_restore_ok(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz, bytea, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_due(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_mark(uuid, uuid, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_followed(uuid, uuid, bytea, timestamptz, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_followup_take(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_caps(uuid, uuid, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_followup_stop(uuid, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_summary(uuid, uuid, date) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_list(uuid, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_today(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_keep(uuid, bytea, bytea), taskiem_public_form_followups(uuid), taskiem_public_form_stop(bytea),
  taskiem_public_form_restore_ok(bytea),
  taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz, bytea, text), taskiem_form_left_due(int),
  taskiem_form_left_mark(uuid, uuid, text), taskiem_form_left_followed(uuid, uuid, bytea, timestamptz, text), taskiem_form_followup_take(bytea),
  taskiem_form_left_caps(uuid, uuid, bytea), taskiem_form_followup_stop(uuid, bytea), taskiem_form_left_summary(uuid, uuid, date),
  taskiem_form_left_list(uuid, uuid), taskiem_form_left_today(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_keep(uuid, bytea, bytea), taskiem_public_form_followups(uuid), taskiem_public_form_stop(bytea),
  taskiem_public_form_restore_ok(bytea) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz, bytea, text), taskiem_form_left_due(int),
  taskiem_form_left_mark(uuid, uuid, text), taskiem_form_left_followed(uuid, uuid, bytea, timestamptz, text), taskiem_form_followup_take(bytea),
  taskiem_form_left_caps(uuid, uuid, bytea), taskiem_form_followup_stop(uuid, bytea), taskiem_form_left_summary(uuid, uuid, date),
  taskiem_form_left_list(uuid, uuid), taskiem_form_left_today(uuid) TO taskiem_app;

-- +goose Down
DROP TRIGGER form_left_erased ON subject_keys;
DROP FUNCTION taskiem_form_left_erased();
DROP FUNCTION taskiem_form_left_today(uuid);
DROP FUNCTION taskiem_form_left_list(uuid, uuid);
DROP FUNCTION taskiem_form_left_summary(uuid, uuid, date);
DROP FUNCTION taskiem_form_followup_stop(uuid, bytea);
DROP FUNCTION taskiem_form_left_caps(uuid, uuid, bytea);
DROP FUNCTION taskiem_form_followup_take(bytea);
DROP FUNCTION taskiem_form_left_mark(uuid, uuid, text);
DROP FUNCTION taskiem_form_left_due(int);
DROP FUNCTION taskiem_public_form_restore_ok(bytea);
DROP FUNCTION taskiem_public_form_stop(bytea);
DROP FUNCTION taskiem_public_form_followups(uuid);

-- L0's functions, as 00800 left them.
DROP FUNCTION taskiem_form_left_followed(uuid, uuid, bytea, timestamptz, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_left_followed(p_tenant uuid, p_id uuid, p_token_hash bytea, p_at timestamptz)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l form_left%ROWTYPE;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  UPDATE form_left SET followed_up_at = p_at, expires_at = p_at + interval '7 days'
   WHERE tenant_id = p_tenant AND id = p_id AND followed_up_at IS NULL
  RETURNING * INTO l;
  IF l.id IS NULL THEN
    RETURN false;
  END IF;
  INSERT INTO form_restores (token_hash, tenant_id, endpoint_id, action_id, choice, expires_at)
  VALUES (p_token_hash, p_tenant, l.endpoint_id, l.action_id, l.choice, p_at + interval '7 days');
  RETURN true;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_form_left_sent(p_tenant uuid, p_scope uuid, p_hash bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  DELETE FROM form_left WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = p_scope AND contact_hash = p_hash AND followed_up_at IS NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n > 0 THEN
    RETURN 'deleted';
  END IF;
  UPDATE form_left SET came_back_at = COALESCE(came_back_at, now())
   WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = p_scope AND contact_hash = p_hash AND followed_up_at IS NOT NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n > 0 THEN
    RETURN 'came_back';
  END IF;
  RETURN '';
END
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
  DELETE FROM form_left WHERE expires_at <= now();
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM form_restores WHERE expires_at <= now();
  GET DIAGNOSTICS m = ROW_COUNT;
  DELETE FROM form_left_inbox WHERE created_at < now() - interval '1 day';
  RETURN n + m;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz, bytea, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_left_put(p_tenant uuid, p_endpoint uuid, p_action uuid, p_front uuid, p_site text, p_contact jsonb,
  p_hash bytea, p_kind text, p_choice jsonb, p_at timestamptz)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  got uuid;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  IF p_endpoint IS NOT NULL AND NOT EXISTS (SELECT 1 FROM form_endpoints WHERE id = p_endpoint AND tenant_id = p_tenant)
     OR p_action IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_actions WHERE id = p_action AND tenant_id = p_tenant)
     OR p_front IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_fronts WHERE id = p_front AND tenant_id = p_tenant) THEN
    RAISE EXCEPTION 'not this business''s form' USING ERRCODE = '42501';
  END IF;
  INSERT INTO form_left (id, tenant_id, endpoint_id, action_id, front_id, site, contact, contact_hash, contact_kind, choice, last_keep_at, expires_at)
  VALUES (gen_random_uuid(), p_tenant, p_endpoint, p_action, p_front, p_site, p_contact, p_hash, p_kind, COALESCE(p_choice, '{}'), p_at, p_at + interval '7 days')
  ON CONFLICT (tenant_id, (COALESCE(endpoint_id, action_id)), contact_hash) DO UPDATE
    SET front_id = EXCLUDED.front_id, site = COALESCE(EXCLUDED.site, form_left.site), contact = EXCLUDED.contact, contact_kind = EXCLUDED.contact_kind,
        choice = EXCLUDED.choice, last_keep_at = GREATEST(form_left.last_keep_at, EXCLUDED.last_keep_at),
        expires_at = CASE WHEN form_left.followed_up_at IS NULL THEN GREATEST(form_left.last_keep_at, EXCLUDED.last_keep_at) + interval '7 days'
                          ELSE form_left.expires_at END
  RETURNING id INTO got;
  RETURN got;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_public_form_keep(uuid, bytea, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_keep(p_endpoint uuid, p_action uuid, p_payload bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  tenant uuid;
BEGIN
  IF (p_endpoint IS NULL) = (p_action IS NULL) THEN
    RETURN false;
  END IF;
  IF p_endpoint IS NOT NULL THEN
    SELECT e.tenant_id INTO tenant FROM form_endpoints e JOIN tenants t ON t.id = e.tenant_id AND t.status = 'active'
     WHERE e.id = p_endpoint AND e.status = 'live';
  ELSE
    SELECT a.tenant_id INTO tenant FROM storefront_actions a JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
      JOIN tenants t ON t.id = a.tenant_id AND t.status = 'active'
     WHERE a.id = p_action AND a.enabled;
  END IF;
  IF tenant IS NULL THEN
    RETURN false;
  END IF;
  IF (SELECT count(*) FROM (SELECT 1 FROM form_left_inbox WHERE tenant_id = tenant LIMIT 2001) x) > 2000 THEN
    RETURN false;
  END IF;
  INSERT INTO form_left_inbox (id, tenant_id, endpoint_id, action_id, payload) VALUES (gen_random_uuid(), tenant, p_endpoint, p_action, p_payload);
  RETURN true;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_form_keep(uuid, uuid, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_followed(uuid, uuid, bytea, timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_keep(uuid, uuid, bytea), taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz),
  taskiem_form_left_followed(uuid, uuid, bytea, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_keep(uuid, uuid, bytea) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz),
  taskiem_form_left_followed(uuid, uuid, bytea, timestamptz) TO taskiem_app;
DROP TRIGGER storefront_actions_followup_default ON storefront_actions;
DROP FUNCTION taskiem_storefront_action_followup_default();
DROP FUNCTION taskiem_form_left_count(uuid, uuid, int, int, int, int);
DROP TABLE form_followup_stops;
DROP TABLE form_followup_contacts;
DROP TABLE form_left_stats;
ALTER TABLE form_restores DROP COLUMN left_id;
DROP INDEX form_left_due;
ALTER TABLE form_left DROP COLUMN page_hash, DROP COLUMN contact_mask, DROP COLUMN left_at, DROP COLUMN outcome, DROP COLUMN channel, DROP COLUMN claimed_until;
ALTER TABLE tenant_limits DROP COLUMN form_followups;
REVOKE SELECT (tenant_id) ON storefront_fronts FROM taskiem_dispatch;
REVOKE SELECT (tenant_id, followup_on, followup_wait, followup_held_at) ON storefront_actions FROM taskiem_dispatch;
ALTER TABLE storefront_actions DROP COLUMN followup_on, DROP COLUMN followup_wait, DROP COLUMN followup_held_at, DROP COLUMN followup_released_at;
