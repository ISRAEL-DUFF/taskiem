-- The customer door opens a business from its listing in Discover
-- (docs/spec/taskiem-for-everyone.md §10, §8.1; stage 3): customer.start
-- is allowed for a business listed in Discover with its Chat button
-- (discover_index.can_chat), while the listing may be shown
-- (taskiem_discover_open: not hidden for reports, the page not
-- suspended). It also opens a business the person has a confirmed
-- storefront order with, found through the phone index (00454, 00458), so
-- "Chat with {business} about this order" works for a business with no
-- published shop; the index key it is given is left out of the audit, as
-- the chat code's hash is. Otherwise the body of 00457, unchanged.
-- +goose Up
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
    -- code's keyed hash, checked here, never a word from the caller), an
    -- order the person placed with it (in the app, or a confirmed one
    -- found by their phone), or its listing in Discover.
    IF NOT (EXISTS (SELECT 1 FROM storefronts WHERE tenant_id = p_tenant AND status = 'published')
        OR EXISTS (SELECT 1 FROM customer_chat_codes c WHERE c.tenant_id = p_tenant AND c.replaced_at IS NULL
                    AND p_detail ? 'code_hash' AND c.code_hash = decode(p_detail ->> 'code_hash', 'hex'))
        OR EXISTS (SELECT 1 FROM storefront_orders WHERE tenant_id = p_tenant AND person_id = p_person)
        -- Listed in Discover with its Chat button (00461): the public may
        -- start a chat from its listing, while the listing may be shown.
        OR (EXISTS (SELECT 1 FROM discover_index d WHERE d.tenant_id = p_tenant AND d.can_chat) AND taskiem_discover_open(p_tenant))
        -- A confirmed storefront order of theirs with it, found through the
        -- phone index by their own verified phone (stage 2's "theirs":
        -- made after the phone was verified, or confirmed with "These are
        -- mine"). The caller gives only the narrowed index key; the hash is
        -- made here from the person's phone, so a wrong key finds nothing.
        OR (p_detail ? 'index_key' AND EXISTS (SELECT 1 FROM taskiem_customer_phone_orders(p_person, decode(p_detail ->> 'index_key', 'hex'), p_tenant, 1)))) THEN
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
    (CASE WHEN jsonb_typeof(p_detail) = 'object' THEN p_detail - 'code_hash' - 'index_key' ELSE COALESCE(p_detail, '{}') END) || jsonb_build_object('door', 'customer'));
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd

-- +goose Down
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
