-- Who pays for a sign-in code through a chat link, and chats with the
-- Orders area off (taskiem-for-everyone §8.2, §8.4, §15; stage 2 review
-- fixes). Founder's decision (2026-11-17):
--
-- * A code asked for through a business's chat link is no longer charged
--   when it is sent: anyone can ask for codes through any link, so a
--   charge on sending let a stranger spend a business's SMS. The code
--   records where it came from and how it went (phone_codes.came_from,
--   sent_via, taskiem_auth_phone_code_source); a NEW account made with it
--   keeps that (customer_signup_sources, taskiem_auth_phone_signup_source),
--   and the business is charged once, when that account then starts a
--   chat with it (customer.start through the door). Codes that lead
--   nowhere, and existing accounts signing in, are Taskiem's.
-- * At most 50 charged codes per business per day (UTC); above that
--   Taskiem pays. Only charged codes are in customer_message_charges, so
--   only they count against the business's SMS allowance.
-- * The customer door: starting or using a chat needs the Orders area on
--   (customer chats belong to it, as /v1/customer-chats does).
-- +goose Up
CREATE TABLE customer_signup_sources (
  -- The account made with a code asked for through a chat link.
  user_id     uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  channel     text NOT NULL CHECK (channel IN ('sms', 'whatsapp')),
  segments    int NOT NULL DEFAULT 1 CHECK (segments > 0),
  created_at  timestamptz NOT NULL DEFAULT now(),
  -- When the account's first chat with the business settled it: charged
  -- to the business, or Taskiem's (over the day's cap).
  settled_at  timestamptz,
  charged     boolean
);
CREATE INDEX customer_signup_sources_old ON customer_signup_sources (created_at) WHERE settled_at IS NULL;
ALTER TABLE customer_signup_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_signup_sources FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON customer_signup_sources TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON customer_signup_sources TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON customer_signup_sources FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_signup_sources TO taskiem_dispatch;

-- The door's charge writes the business's record of it.
CREATE POLICY dispatch ON customer_message_charges TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (tenant_id, purpose, created_at), INSERT ON customer_message_charges TO taskiem_dispatch;

-- Where a pending code was asked for through (a business, or none) and how
-- it went: called after sending, for every sign-in code, so a code asked
-- for again without a link is no longer the business's.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_code_source(p_number text, p_purpose text, p_tenant uuid, p_via text)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH u AS (UPDATE phone_codes c SET came_from = p_tenant, sent_via = CASE WHEN p_via IN ('sms', 'whatsapp') THEN p_via ELSE c.sent_via END
              WHERE c.number = p_number AND c.purpose = p_purpose AND c.expires_at > now() RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM u)
$$;
-- +goose StatementEnd

-- A new account, in its own scope, keeps the business its proving code came
-- through (none: nothing kept).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_signup_source(p_user uuid, p_number text, p_segments int)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a sign-up source is the new account''s own' USING ERRCODE = '42501';
  END IF;
  INSERT INTO customer_signup_sources (user_id, tenant_id, channel, segments)
  SELECT p_user, c.came_from, c.sent_via, GREATEST(COALESCE(p_segments, 1), 1)
    FROM phone_codes c
    JOIN tenants t ON t.id = c.came_from AND t.status = 'active' AND t.kind = 'business'
   WHERE c.number = p_number AND c.purpose = 'sign_in' AND c.proven_at IS NOT NULL
  ON CONFLICT (user_id) DO NOTHING;
  RETURN FOUND;
END
$$;
-- +goose StatementEnd

-- The charge, when a person starts their first chat with a business: once,
-- for an account made through its link, under the day's cap. Called only by
-- the door (not granted to taskiem_app).
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_signup_charge(p_person uuid, p_tenant uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_src customer_signup_sources;
  v_today int;
BEGIN
  SELECT * INTO v_src FROM customer_signup_sources s
   WHERE s.user_id = p_person AND s.tenant_id = p_tenant AND s.settled_at IS NULL FOR UPDATE;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  -- One at a time per business, so the cap is exact.
  PERFORM pg_advisory_xact_lock(hashtextextended('signup-charge:' || p_tenant::text, 0));
  SELECT count(*) INTO v_today FROM customer_message_charges c
   WHERE c.tenant_id = p_tenant AND c.purpose = 'signin_code'
     AND c.created_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC';
  IF v_today >= 50 THEN
    UPDATE customer_signup_sources SET settled_at = now(), charged = false WHERE user_id = p_person;
    RETURN false;
  END IF;
  INSERT INTO customer_message_charges (id, tenant_id, channel, purpose, segments)
  VALUES (gen_random_uuid(), p_tenant, v_src.channel, 'signin_code', v_src.segments);
  UPDATE customer_signup_sources SET settled_at = now(), charged = true WHERE user_id = p_person;
  RETURN true;
END
$$;
-- +goose StatementEnd

-- The door, as 00455 made it, with two changes: starting and using a chat
-- need the Orders area (customer chats are part of it), and a start that
-- opens the person's first chat with the business settles a sign-up code
-- the business owes for.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_enter(p_person uuid, p_tenant uuid, p_action text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_area text[];
  v_thread boolean;
  v_blocked boolean;
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'the customer door is for the person signed in' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = p_tenant AND status = 'active' AND kind = 'business') THEN
    RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
  END IF;
  v_area := CASE p_action
    WHEN 'customer.start' THEN ARRAY['orders']
    WHEN 'customer.chat' THEN ARRAY['orders']
    WHEN 'customer.block' THEN ARRAY[]::text[]
    WHEN 'customer.shop' THEN ARRAY['storefront', 'orders']
    WHEN 'customer.book' THEN ARRAY['storefront', 'orders']
    WHEN 'customer.order' THEN ARRAY['storefront', 'orders']
    WHEN 'customer.booking' THEN ARRAY['storefront', 'orders']
    WHEN 'customer.pay' THEN ARRAY['orders']
    WHEN 'customer.orders' THEN ARRAY['orders']
  END;
  IF v_area IS NULL THEN
    RAISE EXCEPTION 'the customer door has no action %', p_action USING ERRCODE = '22023';
  END IF;
  SELECT true, blocked_at IS NOT NULL INTO v_thread, v_blocked FROM customer_conversations
   WHERE tenant_id = p_tenant AND person_id = p_person AND line = 'app';
  IF COALESCE(v_blocked, false) AND p_action <> 'customer.block' THEN
    RAISE EXCEPTION 'the business does not take messages from this customer' USING ERRCODE = '42501', HINT = 'blocked';
  END IF;
  IF NOT COALESCE(v_thread, false) THEN
    IF p_action <> 'customer.start' THEN
      RAISE EXCEPTION 'no conversation with the business' USING ERRCODE = 'P0002';
    END IF;
    -- Opening one: from its published shop, its live chat link (the
    -- code's keyed hash, checked here, never a word from the caller), or
    -- an order the person placed with it.
    IF NOT (EXISTS (SELECT 1 FROM storefronts WHERE tenant_id = p_tenant AND status = 'published')
        OR EXISTS (SELECT 1 FROM customer_chat_codes c WHERE c.tenant_id = p_tenant AND c.replaced_at IS NULL
                    AND p_detail ? 'code_hash' AND c.code_hash = decode(p_detail ->> 'code_hash', 'hex'))
        OR EXISTS (SELECT 1 FROM storefront_orders WHERE tenant_id = p_tenant AND person_id = p_person)) THEN
      RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
    END IF;
  END IF;
  IF EXISTS (SELECT 1 FROM unnest(v_area) a(area)
      JOIN business_areas b ON b.tenant_id = p_tenant AND b.area = a.area AND NOT b.enabled) THEN
    RAISE EXCEPTION 'the business has turned this off' USING ERRCODE = '55000', HINT = 'area_off';
  END IF;
  -- The person's first chat with the business: a sign-up code it owes for
  -- (an account made through its link) is charged now, once. Rolled back
  -- with the start if the start fails.
  IF p_action = 'customer.start' AND NOT COALESCE(v_thread, false) THEN
    PERFORM taskiem_customer_signup_charge(p_person, p_tenant);
  END IF;
  PERFORM set_config('app.tenant_scope', '{' || p_tenant::text || '}', true);
  PERFORM taskiem_audit_append(p_tenant, 'person', p_person::text, p_action, p_person::text,
    (CASE WHEN jsonb_typeof(p_detail) = 'object' THEN p_detail - 'code_hash' ELSE COALESCE(p_detail, '{}') END) || jsonb_build_object('door', 'customer'));
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_auth_phone_code_came_from(text, text, uuid);

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_auth_phone_code_source(text, text, uuid, text)', 'taskiem_auth_phone_signup_source(uuid, text, int)',
    'taskiem_customer_signup_charge(uuid, uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
  END LOOP;
  FOREACH f IN ARRAY ARRAY['taskiem_auth_phone_code_source(text, text, uuid, text)', 'taskiem_auth_phone_signup_source(uuid, text, int)'] LOOP
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_code_came_from(p_number text, p_purpose text, p_tenant uuid)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH u AS (UPDATE phone_codes c SET came_from = p_tenant
              WHERE c.number = p_number AND c.purpose = p_purpose AND c.expires_at > now() RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM u)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_phone_code_came_from(text, text, uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_phone_code_came_from(text, text, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_phone_code_came_from(text, text, uuid) TO taskiem_app;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_enter(p_person uuid, p_tenant uuid, p_action text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_area text[];
  v_thread boolean;
  v_blocked boolean;
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'the customer door is for the person signed in' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = p_tenant AND status = 'active' AND kind = 'business') THEN
    RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
  END IF;
  v_area := CASE p_action
    WHEN 'customer.start' THEN ARRAY[]::text[]
    WHEN 'customer.chat' THEN ARRAY[]::text[]
    WHEN 'customer.block' THEN ARRAY[]::text[]
    WHEN 'customer.shop' THEN ARRAY['storefront', 'orders']
    WHEN 'customer.book' THEN ARRAY['storefront', 'orders']
    WHEN 'customer.order' THEN ARRAY['storefront', 'orders']
    WHEN 'customer.booking' THEN ARRAY['storefront', 'orders']
    WHEN 'customer.pay' THEN ARRAY['orders']
    WHEN 'customer.orders' THEN ARRAY['orders']
  END;
  IF v_area IS NULL THEN
    RAISE EXCEPTION 'the customer door has no action %', p_action USING ERRCODE = '22023';
  END IF;
  SELECT true, blocked_at IS NOT NULL INTO v_thread, v_blocked FROM customer_conversations
   WHERE tenant_id = p_tenant AND person_id = p_person AND line = 'app';
  IF COALESCE(v_blocked, false) AND p_action <> 'customer.block' THEN
    RAISE EXCEPTION 'the business does not take messages from this customer' USING ERRCODE = '42501', HINT = 'blocked';
  END IF;
  IF NOT COALESCE(v_thread, false) THEN
    IF p_action <> 'customer.start' THEN
      RAISE EXCEPTION 'no conversation with the business' USING ERRCODE = 'P0002';
    END IF;
    IF NOT (EXISTS (SELECT 1 FROM storefronts WHERE tenant_id = p_tenant AND status = 'published')
        OR EXISTS (SELECT 1 FROM customer_chat_codes c WHERE c.tenant_id = p_tenant AND c.replaced_at IS NULL
                    AND p_detail ? 'code_hash' AND c.code_hash = decode(p_detail ->> 'code_hash', 'hex'))
        OR EXISTS (SELECT 1 FROM storefront_orders WHERE tenant_id = p_tenant AND person_id = p_person)) THEN
      RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
    END IF;
  END IF;
  IF EXISTS (SELECT 1 FROM unnest(v_area) a(area)
      JOIN business_areas b ON b.tenant_id = p_tenant AND b.area = a.area AND NOT b.enabled) THEN
    RAISE EXCEPTION 'the business has turned this off' USING ERRCODE = '55000', HINT = 'area_off';
  END IF;
  PERFORM set_config('app.tenant_scope', '{' || p_tenant::text || '}', true);
  PERFORM taskiem_audit_append(p_tenant, 'person', p_person::text, p_action, p_person::text,
    (CASE WHEN jsonb_typeof(p_detail) = 'object' THEN p_detail - 'code_hash' ELSE COALESCE(p_detail, '{}') END) || jsonb_build_object('door', 'customer'));
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_customer_signup_charge(uuid, uuid);
DROP FUNCTION taskiem_auth_phone_signup_source(uuid, text, int);
DROP FUNCTION taskiem_auth_phone_code_source(text, text, uuid, text);
REVOKE SELECT (tenant_id, purpose, created_at), INSERT ON customer_message_charges FROM taskiem_dispatch;
DROP POLICY dispatch ON customer_message_charges;
DROP TABLE customer_signup_sources;
