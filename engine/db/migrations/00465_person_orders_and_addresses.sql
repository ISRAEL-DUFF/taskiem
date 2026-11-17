-- A person's Orders on You and their saved addresses
-- (docs/spec/taskiem-for-everyone.md §13, §14; stage 3, stream B).
--
-- * Orders on You: every order and booking a person has with any business,
--   newest first: those made in the app or the browser while signed in
--   (storefront_orders.person_id) and the storefront orders made with
--   their verified phone that they confirmed ("These are mine", the phone
--   index of 00454 and 00458), each once. Read across businesses only
--   through taskiem_person_orders, which answers exactly the columns the
--   list shows (the business's name and photo, the order's reference,
--   kind, state, a short summary of its lines, total, what is due, whether
--   it can be paid now, its date) for the person in scope, never a
--   business's other data.
-- * One order's detail, and paying it, go through a narrow door of their
--   own, taskiem_person_order_enter: one order of the person's, one
--   business, audited in that business's chain as the customer door is,
--   then that business in scope for the rest of the transaction. It needs
--   no conversation with the business (a storefront order made with the
--   phone has none), unlike taskiem_customer_enter.
-- * Saved addresses (person_addresses): the person's own rows, under
--   taskiem_person_scope() alone. Once any door names a business in the
--   transaction (app.person_business), the saved list is not readable:
--   ordering copies the chosen address before entering, so a business
--   only ever sees the address on its order. Deleted with the account.
-- * An order made in the app belongs to its person: a number's later
--   holder never sees it through the phone index, even after "These are
--   mine" (taskiem_customer_phone_orders and its waiting count).
-- +goose Up
CREATE TABLE person_addresses (
  id          uuid PRIMARY KEY,
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- Home, Work, Mum's: the person's own name for it.
  label       text NOT NULL CHECK (length(label) BETWEEN 1 AND 40),
  line        text NOT NULL CHECK (length(line) BETWEEN 1 AND 200),
  area        text NOT NULL DEFAULT '' CHECK (length(area) <= 80),
  landmark    text NOT NULL DEFAULT '' CHECK (length(landmark) <= 200),
  -- A phone for the rider, when it is not the person's own (E.164).
  phone       text CHECK (phone IS NULL OR phone ~ '^\+[1-9][0-9]{6,14}$'),
  is_default  boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX person_addresses_person ON person_addresses (person_id, created_at);
CREATE UNIQUE INDEX person_addresses_default ON person_addresses (person_id) WHERE is_default;
ALTER TABLE person_addresses ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_addresses FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON person_addresses TO taskiem_app
  USING (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
GRANT SELECT, INSERT, UPDATE, DELETE ON person_addresses TO taskiem_app;

-- A person's app orders across businesses, newest first.
CREATE INDEX storefront_orders_person_any ON storefront_orders (person_id, created_at DESC) WHERE person_id IS NOT NULL;

-- The list's summary of lines and what is due.
GRANT SELECT (basket, extra_kobo, paid_kobo) ON storefront_orders TO taskiem_dispatch;

-- The orders that are a person's: made in the app while signed in, or
-- made with their verified phone (the index) and not waiting for "These
-- are mine", and not an app order of someone else. Not callable by the
-- application: it runs inside the definers below, as their owner.
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

-- Orders on You: the person's own orders and bookings with every business
-- (or one business's, or one order), newest first, after a cursor (the
-- last row's date and reference), each once. Only these columns, and only
-- for the person in scope. p_key is the phone index's key (00454); NULL
-- leaves the phone's orders out.
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
         -- Pay: something is due now and it can be paid in the app; never
         -- while the business blocked the person or turned Orders off.
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

-- The door to one of the person's orders (its detail, paying it): one
-- business, one order of the person's (taskiem_person_order_ids), one
-- action; refused when the business blocked the person or turned Orders
-- off. Audited in the business's chain as the customer door is (the
-- index key never), then that business is in scope for the rest of the
-- transaction, with app.person_business naming it (so the person's other
-- businesses' rows and their saved addresses are out of reach).
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

-- An app order is its person's: the phone index never lists it to the
-- number's later holder (00458's bodies, with that one condition).
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

-- +goose Down
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
     AND NOT (o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL)
   ORDER BY o.created_at DESC LIMIT LEAST(GREATEST(p_limit, 1), 200);
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_person_order_enter(uuid, bytea, uuid, text, text);
DROP FUNCTION taskiem_person_orders(uuid, bytea, uuid, text, timestamptz, text, int);
DROP FUNCTION taskiem_person_order_ids(uuid, bytea);
REVOKE SELECT (basket, extra_kobo, paid_kobo) ON storefront_orders FROM taskiem_dispatch;
DROP INDEX storefront_orders_person_any;
DROP TABLE person_addresses;
