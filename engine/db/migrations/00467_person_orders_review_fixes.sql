-- Orders on You and the person's account: review fixes and what was left
-- (docs/spec/taskiem-for-everyone.md §7, §13, §14; "As built: stage 3,
-- Orders and account review fixes").
--
-- * One rule for "can this be paid now", in SQL
--   (taskiem_order_payable_now) and in Go (storefront.PayableNow), pinned
--   to each other by a test: the list's Pay and the pay links agree, and a
--   cancelled, declined, expired or refunded order is never handed a link.
-- * Earlier orders on a reissued number (founder's decision A): a
--   storefront order found only through the phone index, made before the
--   phone was verified on this account, is shown in the short view only
--   (business, reference, state, total, date), even after "These are
--   mine": no lines in its summary, no detail through the order door, and
--   no Pay. taskiem_person_order_ids says which (earlier), the list
--   answers it, and the order door refuses them (hint earlier).
-- * Deleting an account takes its orders out of the phone index
--   (founder's decision B): a BEFORE DELETE trigger on users, and the
--   deletion path (taskiem_person_delete), mark the account's app orders
--   (storefront_orders.person_left) and remove their index rows, and the
--   deletion path also removes every row under the account's phone. An
--   order marked so is never claimable through the phone, whoever holds
--   the number next.
-- * waiting_count counts what "These are mine" then shows: the list's
--   filters (forgotten, kinds, businesses only).
-- * Hire, spaces, classes and tickets are bookings on Orders, their
--   summary naming what was booked; booking_type says which.
-- * The business's photo on Orders leaves out a published page's logo
--   the moderators rejected (Discover's stream adds one shared function
--   for this; this is the same check, to be merged with it).
-- * Deleting an account (§14): taskiem_person_delete, for the person in
--   scope, with no business of their own or role in one.
-- * You's settings: the person's photo (person_photos) and which pushes
--   they get (person_notify), their own rows.
-- * The privacy page: the businesses that hold the person's number
--   through their orders (taskiem_person_businesses).
-- +goose Up

-- The phone code that confirms deleting an account.
ALTER TABLE phone_codes DROP CONSTRAINT phone_codes_purpose_check;
ALTER TABLE phone_codes ADD CONSTRAINT phone_codes_purpose_check CHECK (purpose IN ('sign_in', 'change_new', 'change_old', 'delete'));

-- When the account was deleted: the row is kept, emptied (no name, email,
-- phone or password), so the businesses' records that name who did what
-- keep their keys.
ALTER TABLE users ADD COLUMN deleted_at timestamptz;

-- An order of an account that was deleted: never claimable through the
-- phone index again (its person_id is cleared, or set null by the foreign
-- key when the row itself is deleted).
ALTER TABLE storefront_orders ADD COLUMN person_left boolean NOT NULL DEFAULT false;
GRANT SELECT (person_left), UPDATE (person_id, person_left) ON storefront_orders TO taskiem_dispatch;

-- The business's thread with a customer who deleted their account: it
-- says "This customer left Taskiem" and takes no more messages.
ALTER TABLE customer_conversations ADD COLUMN person_left_at timestamptz;
CREATE POLICY dispatch_person_left ON customer_conversations FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT UPDATE (person_id, person_left_at) ON customer_conversations TO taskiem_dispatch;

-- An account deleted takes its passkeys with it.
GRANT DELETE ON webauthn_credentials TO taskiem_dispatch;

-- The person's photo, on their own side only: businesses are not shown
-- it. Cleaned by engine/media (re-encoded, so no metadata) before it is
-- kept.
CREATE TABLE person_photos (
  person_id   uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  type        text NOT NULL CHECK (type IN ('image/jpeg', 'image/png')),
  data        bytea NOT NULL CHECK (length(data) BETWEEN 1 AND 2097152),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE person_photos ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_photos FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON person_photos TO taskiem_app
  USING (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
GRANT SELECT, INSERT, UPDATE, DELETE ON person_photos TO taskiem_app;

-- Which pushes the person gets on You: a business's messages, its
-- automatic updates (orders, bookings), its offers. No row: all on.
CREATE TABLE person_notify (
  person_id   uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  messages    boolean NOT NULL DEFAULT true,
  updates     boolean NOT NULL DEFAULT true,
  offers      boolean NOT NULL DEFAULT true,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE person_notify ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_notify FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON person_notify TO taskiem_app
  USING (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
GRANT SELECT, INSERT, UPDATE, DELETE ON person_notify TO taskiem_app;

-- Whether an order can be paid now, by its status, how it is paid and
-- what is left to pay. storefront.PayableNow is the same rule in Go
-- (TestPayableNowMatchesSQL).
-- +goose StatementBegin
CREATE FUNCTION taskiem_order_payable_now(p_status text, p_payment text, p_due bigint)
RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
  SELECT COALESCE(p_due, 0) > 0 AND COALESCE(p_payment, 'none') <> 'none'
     AND p_status IN ('awaiting_payment', 'accepted', 'received', 'paid', 'dispatched', 'on_the_way', 'arrived')
     AND NOT (p_status = 'received' AND p_payment = 'after_accept')
$$;
-- +goose StatementEnd

-- The orders that are a person's, and which of them are earlier: found
-- only through the phone index and made before the phone was verified on
-- this account (decision A). An order of a deleted account (person_left)
-- is never claimable through the phone.
DROP FUNCTION taskiem_person_order_ids(uuid, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_order_ids(p_user uuid, p_key bytea)
RETURNS TABLE (order_id uuid, earlier boolean)
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT x.id, bool_and(x.earlier) FROM (
    SELECT o.id, false AS earlier FROM storefront_orders o WHERE o.person_id = p_user
    UNION ALL
    SELECT o.id, o.created_at < p.verified_at
      FROM user_phones p
      JOIN customer_phone_index i ON i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'))
      JOIN storefront_orders o ON o.id = i.order_id AND o.tenant_id = i.tenant_id
     WHERE p.user_id = p_user AND length(p_key) = 32
       AND (o.person_id IS NULL AND NOT o.person_left OR o.person_id = p_user)
       AND NOT (o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL)) x
   GROUP BY x.id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_person_order_ids(uuid, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_person_order_ids(uuid, bytea) FROM PUBLIC;

-- Orders on You, with earlier (the short view: no summary, nothing due,
-- no Pay) and booking_type (service, hire, space, class, tickets); kind
-- is booking for each of these. The photo leaves out a published page's
-- logo the moderators rejected.
DROP FUNCTION taskiem_person_orders(uuid, bytea, uuid, text, timestamptz, text, int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_orders(p_user uuid, p_key bytea, p_business uuid, p_reference text,
  p_before_at timestamptz, p_before_ref text, p_limit int)
RETURNS TABLE (business_id uuid, business text, photo jsonb, reference text, kind text, booking_type text, state text, summary text,
  total_kobo bigint, due_kobo bigint, pay boolean, made_at timestamptz, earlier boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  WITH mine AS (
    SELECT o.tenant_id, o.reference, o.kind, o.status, o.service_name, o.total_kobo, o.extra_kobo, o.payment, o.created_at,
           m.earlier AS was_earlier,
           GREATEST(o.total_kobo + o.extra_kobo - o.paid_kobo, 0) AS left_kobo,
           (SELECT sum(CASE WHEN l ->> 'quantity' ~ '^[0-9]{1,6}$' THEN (l ->> 'quantity')::int ELSE 1 END)
              FROM jsonb_array_elements(CASE WHEN jsonb_typeof(o.basket) = 'array' THEN o.basket ELSE '[]'::jsonb END) l) AS qty,
           CASE WHEN o.starts_at IS NOT NULL AND o.ends_at IS NOT NULL
                THEN GREATEST(round(extract(epoch FROM o.ends_at - o.starts_at) / 86400)::int, 1) END AS days,
           COALESCE((
             SELECT string_agg(COALESCE(l ->> 'quantity', '1') || ' × ' || COALESCE(l ->> 'name', '')
                      || COALESCE(' (' || (SELECT string_agg(op ->> 'choice', ', ')
                                             FROM jsonb_array_elements(CASE WHEN jsonb_typeof(l -> 'options') = 'array' THEN l -> 'options' ELSE '[]'::jsonb END) op) || ')', ''),
                      ', ' ORDER BY n)
               FROM jsonb_array_elements(CASE WHEN jsonb_typeof(o.basket) = 'array' THEN o.basket ELSE '[]'::jsonb END) WITH ORDINALITY x(l, n)), '') AS lines
      FROM taskiem_person_order_ids(p_user, p_key) m
      JOIN storefront_orders o ON o.id = m.order_id
     WHERE o.forgotten_at IS NULL
       AND o.kind NOT IN ('enquiry', 'callback', 'quote', 'trade')
       AND (p_business IS NULL OR o.tenant_id = p_business)
       AND (p_reference IS NULL OR o.reference = p_reference)
       AND (p_before_at IS NULL OR (o.created_at, o.reference) < (p_before_at, COALESCE(p_before_ref, '')))
  )
  SELECT t.id, t.name,
         COALESCE(
           (SELECT med.urls FROM business_profile bp JOIN storefront_media med ON med.tenant_id = bp.tenant_id AND med.id = bp.logo_media
             WHERE bp.tenant_id = t.id AND med.moderation <> 'rejected' AND jsonb_typeof(med.urls) = 'object'),
           -- The published page's logo, unless the moderators rejected
           -- its picture since (the snapshot keeps its media id).
           (SELECT snap.model -> 'logo' FROM storefronts s
              JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
             WHERE s.tenant_id = t.id AND s.status = 'published' AND jsonb_typeof(snap.model -> 'logo') = 'object'
               AND snap.model -> 'logo' <> '{}'::jsonb
               AND EXISTS (SELECT 1 FROM storefront_media med WHERE med.tenant_id = t.id AND med.id::text = snap.model -> 'card' ->> 'logo'
                             AND med.moderation <> 'rejected'))),
         o.reference,
         CASE WHEN o.kind IN ('booking', 'hire', 'space', 'class', 'tickets') THEN 'booking' ELSE 'order' END,
         CASE o.kind WHEN 'booking' THEN 'service' WHEN 'hire' THEN 'hire' WHEN 'space' THEN 'space'
                     WHEN 'class' THEN 'class' WHEN 'tickets' THEN 'tickets' END,
         o.status,
         CASE WHEN o.was_earlier THEN '' ELSE left(CASE
           WHEN o.kind = 'booking' THEN COALESCE(NULLIF(o.service_name, ''), o.lines)
           WHEN o.kind = 'hire' AND COALESCE(o.service_name, '') <> '' THEN
             CASE WHEN COALESCE(o.days, 1) > 0 AND COALESCE(o.qty, 1) / COALESCE(o.days, 1) > 1
                  THEN (o.qty / o.days)::text || ' × ' ELSE '' END
             || o.service_name || COALESCE(', ' || o.days || CASE WHEN o.days = 1 THEN ' day' ELSE ' days' END, '')
           WHEN o.kind = 'tickets' AND COALESCE(o.service_name, '') <> '' THEN
             COALESCE(o.qty, 1)::text || CASE WHEN COALESCE(o.qty, 1) = 1 THEN ' ticket, ' ELSE ' tickets, ' END || o.service_name
           WHEN o.kind IN ('space', 'class') AND COALESCE(o.service_name, '') <> '' THEN
             CASE WHEN o.kind = 'class' AND COALESCE(o.qty, 1) > 1 THEN o.qty::text || ' places, ' ELSE '' END || o.service_name
           ELSE o.lines END, 200) END,
         o.total_kobo + o.extra_kobo,
         CASE WHEN o.was_earlier THEN 0::bigint ELSE o.left_kobo END,
         -- Pay: by the one rule; never for an earlier order, nor while the
         -- business blocked the person or turned Orders off.
         (NOT o.was_earlier AND taskiem_order_payable_now(o.status, o.payment, o.left_kobo)
           AND NOT EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = t.id AND c.person_id = p_user AND c.line = 'app' AND c.blocked_at IS NOT NULL)
           AND NOT EXISTS (SELECT 1 FROM business_areas b WHERE b.tenant_id = t.id AND b.area = 'orders' AND NOT b.enabled)),
         o.created_at,
         o.was_earlier
    FROM mine o
    JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active' AND t.kind = 'business'
    JOIN users u ON u.id = p_user AND u.status = 'active'
   ORDER BY o.created_at DESC, o.reference DESC
   LIMIT LEAST(GREATEST(p_limit, 1), 101);
END
$$;
-- +goose StatementEnd

-- The order door: as 00465's, and an earlier order is not opened (hint
-- earlier): its detail and paying it are not for this account.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_person_order_enter(p_person uuid, p_key bytea, p_tenant uuid, p_reference text, p_action text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_earlier boolean;
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'the customer door is for the person signed in' USING ERRCODE = '42501';
  END IF;
  IF current_setting('app.person_business', true) IS NOT NULL AND current_setting('app.person_business', true) <> '' THEN
    RAISE EXCEPTION 'a door is already open in this transaction' USING ERRCODE = '42501';
  END IF;
  IF p_action NOT IN ('customer.order_detail', 'customer.pay') THEN
    RAISE EXCEPTION 'the order door has no action %', p_action USING ERRCODE = '22023';
  END IF;
  SELECT mine.earlier INTO v_earlier
    FROM storefront_orders o JOIN taskiem_person_order_ids(p_person, p_key) mine ON mine.order_id = o.id
   WHERE o.tenant_id = p_tenant AND o.reference = p_reference AND o.forgotten_at IS NULL
     AND o.kind NOT IN ('enquiry', 'callback', 'quote', 'trade');
  IF NOT FOUND
     OR NOT EXISTS (SELECT 1 FROM tenants WHERE id = p_tenant AND status = 'active' AND kind = 'business')
     OR NOT EXISTS (SELECT 1 FROM users WHERE id = p_person AND status = 'active') THEN
    RAISE EXCEPTION 'no such order' USING ERRCODE = 'P0002';
  END IF;
  IF v_earlier THEN
    RAISE EXCEPTION 'this order was made with the number before it was on this account' USING ERRCODE = '42501', HINT = 'earlier';
  END IF;
  IF EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = p_tenant AND c.person_id = p_person AND c.line = 'app' AND c.blocked_at IS NOT NULL) THEN
    RAISE EXCEPTION 'the business does not take messages from this customer' USING ERRCODE = '42501', HINT = 'blocked';
  END IF;
  IF EXISTS (SELECT 1 FROM business_areas b WHERE b.tenant_id = p_tenant AND b.area = 'orders' AND NOT b.enabled) THEN
    RAISE EXCEPTION 'the business has turned this off' USING ERRCODE = '55000', HINT = 'area_off';
  END IF;
  PERFORM set_config('app.tenant_scope', '{' || p_tenant::text || '}', true);
  PERFORM taskiem_audit_append(p_tenant, 'person', p_person::text, p_action, p_person::text,
    jsonb_build_object('reference', p_reference, 'door', 'customer', 'from', 'orders'));
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd

-- The phone's orders (stage 2's list, the door's phone-order check): an
-- order of a deleted account is never another holder's.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_phone_orders(p_user uuid, p_key bytea, p_business uuid, p_limit int)
RETURNS TABLE (reference text, state text, total_kobo bigint, made_at timestamptz, business text, business_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT o.reference, o.status, o.total_kobo, o.created_at, t.name, t.id
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active'
    JOIN customer_phone_index i ON i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'))
    JOIN storefront_orders o ON o.id = i.order_id AND o.tenant_id = i.tenant_id
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE p.user_id = p_user AND length(p_key) = 32
     AND (p_business IS NULL OR i.tenant_id = p_business)
     AND (o.person_id IS NULL AND NOT o.person_left OR o.person_id = p_user)
     AND NOT (o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL)
   ORDER BY o.created_at DESC LIMIT LEAST(GREATEST(p_limit, 1), 200);
END
$$;
-- +goose StatementEnd

-- What "These are mine" would add to Orders on You: the list's filters
-- (not forgotten, not an enquiry, callback, quote or trade order, an
-- active business), and never another person's or a deleted account's
-- app order.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_phone_orders_waiting(p_user uuid, p_key bytea)
RETURNS int LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_n int;
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  SELECT count(*) INTO v_n
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active'
    JOIN customer_phone_index i ON i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'))
    JOIN storefront_orders o ON o.id = i.order_id AND o.tenant_id = i.tenant_id
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active' AND t.kind = 'business'
   WHERE p.user_id = p_user AND length(p_key) = 32
     AND (o.person_id IS NULL AND NOT o.person_left OR o.person_id = p_user)
     AND o.forgotten_at IS NULL
     AND o.kind NOT IN ('enquiry', 'callback', 'quote', 'trade')
     AND o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL;
  RETURN v_n;
END
$$;
-- +goose StatementEnd

-- An account's app orders leave the phone index, and are marked as never
-- claimable through the phone (decision B). Not callable by the
-- application: the trigger below and taskiem_person_delete run it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_orders_release(p_user uuid)
RETURNS bigint LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  DELETE FROM customer_phone_index i USING storefront_orders o
   WHERE o.id = i.order_id AND o.tenant_id = i.tenant_id AND o.person_id = p_user;
  GET DIAGNOSTICS n = ROW_COUNT;
  UPDATE storefront_orders o SET person_left = true WHERE o.person_id = p_user AND NOT o.person_left;
  RETURN n;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_person_orders_release(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_person_orders_release(uuid) FROM PUBLIC;

-- A users row deleted, however (an operator's hand, a test): its app
-- orders are released before the foreign key clears person_id.
-- +goose StatementBegin
CREATE FUNCTION taskiem_users_before_delete() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_person_orders_release(OLD.id);
  RETURN OLD;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_users_before_delete() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_users_before_delete() FROM PUBLIC;
CREATE TRIGGER users_before_delete BEFORE DELETE ON users FOR EACH ROW EXECUTE FUNCTION taskiem_users_before_delete();

-- Deleting an account (§14): the person in scope, in no business (an
-- owner closes theirs or hands it over first; a member is removed by its
-- owner): every row of the phone index under their phone and for their
-- app orders goes, their app orders are marked (decision B) and no longer
-- name them, the businesses' threads with them no longer name them and
-- say they left, every session ends, and their phone, devices, second
-- proofs, codes, passkeys and chat bindings go. The caller empties the
-- users row, deletes their own side (chats, files, addresses, devices for
-- pushes, photo, settings) and writes "This customer left Taskiem" in each
-- business's thread in the same transaction. Answers the index rows
-- removed.
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_delete(p_person uuid, p_key bytea)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint := 0;
  m bigint;
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'an account is deleted by its own person' USING ERRCODE = '42501';
  END IF;
  IF current_setting('app.person_business', true) IS NOT NULL AND current_setting('app.person_business', true) <> '' THEN
    RAISE EXCEPTION 'a door is open in this transaction' USING ERRCODE = '42501';
  END IF;
  PERFORM 1 FROM users WHERE id = p_person AND status = 'active';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such account' USING ERRCODE = 'P0002';
  END IF;
  IF EXISTS (SELECT 1 FROM memberships m JOIN tenants t ON t.id = m.tenant_id AND t.status <> 'deleted'
              WHERE m.user_id = p_person AND m.role = 'owner') THEN
    RAISE EXCEPTION 'the person owns a business' USING ERRCODE = '55000', HINT = 'owns_business';
  END IF;
  IF EXISTS (SELECT 1 FROM memberships m JOIN tenants t ON t.id = m.tenant_id AND t.status <> 'deleted'
              WHERE m.user_id = p_person) THEN
    RAISE EXCEPTION 'the person has a role in a business' USING ERRCODE = '55000', HINT = 'in_business';
  END IF;
  -- Every index row under their phone (orders made with it, theirs or
  -- not yet claimed), then their app orders' rows, wherever indexed.
  IF length(p_key) = 32 THEN
    DELETE FROM customer_phone_index i USING user_phones p
     WHERE p.user_id = p_person AND i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'));
    GET DIAGNOSTICS m = ROW_COUNT;
    n := n + m;
  END IF;
  n := n + taskiem_person_orders_release(p_person);
  UPDATE storefront_orders o SET person_id = NULL WHERE o.person_id = p_person;
  UPDATE customer_conversations c SET person_id = NULL, person_left_at = now() WHERE c.person_id = p_person;
  UPDATE sessions s SET revoked_at = now() WHERE s.user_id = p_person AND s.revoked_at IS NULL;
  DELETE FROM user_phones WHERE user_id = p_person;
  DELETE FROM user_devices WHERE user_id = p_person;
  DELETE FROM second_proof_links WHERE user_id = p_person;
  DELETE FROM phone_codes WHERE user_id = p_person;
  DELETE FROM webauthn_credentials WHERE user_id = p_person;
  DELETE FROM whatsapp_pins WHERE user_id = p_person;
  DELETE FROM whatsapp_bindings WHERE user_id = p_person;
  DELETE FROM telegram_bindings WHERE user_id = p_person;
  RETURN n;
END
$$;
-- +goose StatementEnd

-- The businesses that hold the person's number through their orders, for
-- the privacy page: the business, how many of the person's orders, the
-- latest. The person in scope only; the same orders as Orders on You
-- (not those waiting for "These are mine").
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_businesses(p_user uuid, p_key bytea)
RETURNS TABLE (business_id uuid, business text, orders int, last_order_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT t.id, t.name, count(*)::int, max(o.created_at)
    FROM taskiem_person_order_ids(p_user, p_key) m
    JOIN storefront_orders o ON o.id = m.order_id
    JOIN tenants t ON t.id = o.tenant_id AND t.status <> 'deleted' AND t.kind = 'business'
   WHERE o.forgotten_at IS NULL AND o.kind <> 'trade'
   GROUP BY t.id, t.name
   ORDER BY max(o.created_at) DESC
   LIMIT 500;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_person_orders(uuid, bytea, uuid, text, timestamptz, text, int)',
                           'taskiem_person_delete(uuid, bytea)',
                           'taskiem_person_businesses(uuid, bytea)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_person_businesses(uuid, bytea);
DROP FUNCTION taskiem_person_delete(uuid, bytea);
DROP TRIGGER users_before_delete ON users;
DROP FUNCTION taskiem_users_before_delete();
DROP FUNCTION taskiem_person_orders_release(uuid);

-- 00465's bodies.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_phone_orders_waiting(p_user uuid, p_key bytea)
RETURNS int LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_n int;
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  SELECT count(*) INTO v_n
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active'
    JOIN customer_phone_index i ON i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'))
    JOIN storefront_orders o ON o.id = i.order_id AND o.tenant_id = i.tenant_id
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE p.user_id = p_user AND length(p_key) = 32
     AND (o.person_id IS NULL OR o.person_id = p_user)
     AND o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL;
  RETURN v_n;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_phone_orders(p_user uuid, p_key bytea, p_business uuid, p_limit int)
RETURNS TABLE (reference text, state text, total_kobo bigint, made_at timestamptz, business text, business_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT o.reference, o.status, o.total_kobo, o.created_at, t.name, t.id
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active'
    JOIN customer_phone_index i ON i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'))
    JOIN storefront_orders o ON o.id = i.order_id AND o.tenant_id = i.tenant_id
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE p.user_id = p_user AND length(p_key) = 32
     AND (p_business IS NULL OR i.tenant_id = p_business)
     AND (o.person_id IS NULL OR o.person_id = p_user)
     AND NOT (o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL)
   ORDER BY o.created_at DESC LIMIT LEAST(GREATEST(p_limit, 1), 200);
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_person_order_enter(uuid, bytea, uuid, text, text);
DROP FUNCTION taskiem_person_orders(uuid, bytea, uuid, text, timestamptz, text, int);
DROP FUNCTION taskiem_person_order_ids(uuid, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_order_ids(p_user uuid, p_key bytea)
RETURNS TABLE (order_id uuid)
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT o.id FROM storefront_orders o WHERE o.person_id = p_user
  UNION
  SELECT o.id
    FROM user_phones p
    JOIN customer_phone_index i ON i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'))
    JOIN storefront_orders o ON o.id = i.order_id AND o.tenant_id = i.tenant_id
   WHERE p.user_id = p_user AND length(p_key) = 32
     AND (o.person_id IS NULL OR o.person_id = p_user)
     AND NOT (o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_person_order_ids(uuid, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_person_order_ids(uuid, bytea) FROM PUBLIC;
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_orders(p_user uuid, p_key bytea, p_business uuid, p_reference text,
  p_before_at timestamptz, p_before_ref text, p_limit int)
RETURNS TABLE (business_id uuid, business text, photo jsonb, reference text, kind text, state text, summary text,
  total_kobo bigint, due_kobo bigint, pay boolean, made_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT t.id, t.name,
         COALESCE(
           (SELECT m.urls FROM business_profile bp JOIN storefront_media m ON m.tenant_id = bp.tenant_id AND m.id = bp.logo_media
             WHERE bp.tenant_id = t.id AND m.moderation <> 'rejected' AND jsonb_typeof(m.urls) = 'object'),
           (SELECT snap.model -> 'logo' FROM storefronts s
              JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
             WHERE s.tenant_id = t.id AND s.status = 'published' AND jsonb_typeof(snap.model -> 'logo') = 'object'
               AND snap.model -> 'logo' <> '{}'::jsonb)),
         o.reference,
         CASE WHEN o.kind = 'booking' THEN 'booking' ELSE 'order' END,
         o.status,
         left(CASE WHEN o.kind = 'booking' THEN COALESCE(o.service_name, '') ELSE COALESCE((
           SELECT string_agg(COALESCE(l ->> 'quantity', '1') || ' × ' || COALESCE(l ->> 'name', '')
                    || COALESCE(' (' || (SELECT string_agg(op ->> 'choice', ', ')
                                           FROM jsonb_array_elements(CASE WHEN jsonb_typeof(l -> 'options') = 'array' THEN l -> 'options' ELSE '[]'::jsonb END) op) || ')', ''),
                    ', ' ORDER BY n)
             FROM jsonb_array_elements(CASE WHEN jsonb_typeof(o.basket) = 'array' THEN o.basket ELSE '[]'::jsonb END) WITH ORDINALITY x(l, n)), '') END, 200),
         o.total_kobo + o.extra_kobo,
         GREATEST(o.total_kobo + o.extra_kobo - o.paid_kobo, 0),
         (o.total_kobo + o.extra_kobo - o.paid_kobo > 0 AND o.payment <> 'none'
           AND o.status IN ('awaiting_payment', 'accepted', 'received', 'paid')
           AND NOT (o.status = 'received' AND o.payment = 'after_accept')
           AND NOT EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = t.id AND c.person_id = p_user AND c.line = 'app' AND c.blocked_at IS NOT NULL)
           AND NOT EXISTS (SELECT 1 FROM business_areas b WHERE b.tenant_id = t.id AND b.area = 'orders' AND NOT b.enabled)),
         o.created_at
    FROM taskiem_person_order_ids(p_user, p_key) mine
    JOIN storefront_orders o ON o.id = mine.order_id
    JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active' AND t.kind = 'business'
    JOIN users u ON u.id = p_user AND u.status = 'active'
   WHERE o.forgotten_at IS NULL
     AND o.kind NOT IN ('enquiry', 'callback', 'quote', 'trade')
     AND (p_business IS NULL OR o.tenant_id = p_business)
     AND (p_reference IS NULL OR o.reference = p_reference)
     AND (p_before_at IS NULL OR (o.created_at, o.reference) < (p_before_at, COALESCE(p_before_ref, '')))
   ORDER BY o.created_at DESC, o.reference DESC
   LIMIT LEAST(GREATEST(p_limit, 1), 101);
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_order_enter(p_person uuid, p_key bytea, p_tenant uuid, p_reference text, p_action text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'the customer door is for the person signed in' USING ERRCODE = '42501';
  END IF;
  IF current_setting('app.person_business', true) IS NOT NULL AND current_setting('app.person_business', true) <> '' THEN
    RAISE EXCEPTION 'a door is already open in this transaction' USING ERRCODE = '42501';
  END IF;
  IF p_action NOT IN ('customer.order_detail', 'customer.pay') THEN
    RAISE EXCEPTION 'the order door has no action %', p_action USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = p_tenant AND status = 'active' AND kind = 'business')
     OR NOT EXISTS (SELECT 1 FROM users WHERE id = p_person AND status = 'active')
     OR NOT EXISTS (SELECT 1 FROM storefront_orders o JOIN taskiem_person_order_ids(p_person, p_key) mine ON mine.order_id = o.id
                     WHERE o.tenant_id = p_tenant AND o.reference = p_reference AND o.forgotten_at IS NULL
                       AND o.kind NOT IN ('enquiry', 'callback', 'quote', 'trade')) THEN
    RAISE EXCEPTION 'no such order' USING ERRCODE = 'P0002';
  END IF;
  IF EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = p_tenant AND c.person_id = p_person AND c.line = 'app' AND c.blocked_at IS NOT NULL) THEN
    RAISE EXCEPTION 'the business does not take messages from this customer' USING ERRCODE = '42501', HINT = 'blocked';
  END IF;
  IF EXISTS (SELECT 1 FROM business_areas b WHERE b.tenant_id = p_tenant AND b.area = 'orders' AND NOT b.enabled) THEN
    RAISE EXCEPTION 'the business has turned this off' USING ERRCODE = '55000', HINT = 'area_off';
  END IF;
  PERFORM set_config('app.tenant_scope', '{' || p_tenant::text || '}', true);
  PERFORM taskiem_audit_append(p_tenant, 'person', p_person::text, p_action, p_person::text,
    jsonb_build_object('reference', p_reference, 'door', 'customer', 'from', 'orders'));
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_person_orders(uuid, bytea, uuid, text, timestamptz, text, int)',
                           'taskiem_person_order_enter(uuid, bytea, uuid, text, text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd
DROP FUNCTION taskiem_order_payable_now(text, text, bigint);
DROP TABLE person_notify;
DROP TABLE person_photos;
REVOKE DELETE ON webauthn_credentials FROM taskiem_dispatch;
REVOKE UPDATE (person_id, person_left_at) ON customer_conversations FROM taskiem_dispatch;
DROP POLICY dispatch_person_left ON customer_conversations;
ALTER TABLE customer_conversations DROP COLUMN person_left_at;
REVOKE SELECT (person_left), UPDATE (person_id, person_left) ON storefront_orders FROM taskiem_dispatch;
ALTER TABLE storefront_orders DROP COLUMN person_left;
ALTER TABLE users DROP COLUMN deleted_at;
DELETE FROM phone_codes WHERE purpose = 'delete';
ALTER TABLE phone_codes DROP CONSTRAINT phone_codes_purpose_check;
ALTER TABLE phone_codes ADD CONSTRAINT phone_codes_purpose_check CHECK (purpose IN ('sign_in', 'change_new', 'change_old'));
