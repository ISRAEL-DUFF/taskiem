-- The customer door checks a chat link's code itself (taskiem-for-everyone
-- §8.2, §8.4; stage 2 merge of streams A and B). 00452 opened a business
-- to anyone whose detail said via = 'code', trusting the API; the API
-- also took that word from the person, so any business could be opened by
-- its id. Now customer.start from a link carries the code's keyed hash
-- (engine/chatlinks), and the door opens only when it is the business's
-- live code. The hash is kept out of the audit entry.
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
  PERFORM set_config('app.tenant_scope', '{' || p_tenant::text || '}', true);
  PERFORM taskiem_audit_append(p_tenant, 'person', p_person::text, p_action, p_person::text,
    (CASE WHEN jsonb_typeof(p_detail) = 'object' THEN p_detail - 'code_hash' ELSE COALESCE(p_detail, '{}') END) || jsonb_build_object('door', 'customer'));
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
    -- Opening one: from its published shop, its chat link (the API
    -- resolved the code), or an order the person placed with it.
    IF NOT (EXISTS (SELECT 1 FROM storefronts WHERE tenant_id = p_tenant AND status = 'published')
        OR COALESCE(p_detail ->> 'via', '') = 'code'
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
    COALESCE(p_detail, '{}') || jsonb_build_object('door', 'customer'));
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd
