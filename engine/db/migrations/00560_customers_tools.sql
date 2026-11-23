-- Customers' tools (made tools T5, decision 0070 point 6; the build plan's
-- M5). The public menu becomes customers' tools:
--
-- * Each entry of a public menu (whatsapp_public_workflows, up to nine a
--   business) is carried over as a customers' tool of its workflow, named
--   by its label, with a command made from the label here (unique in the
--   business, and never a command a deployed prod tool of another workflow
--   has), so WhatsApp and the app reach it as they reach a tool. A
--   workflow that declares tool with for "customers" is that tool instead
--   of its menu entry. Only entries with no command are given one.
-- * customer_tool_watches: a run a customer started on a business's own
--   WhatsApp number, whose answer goes back to that number when it ends
--   (the number is the customer's address there, as the public sessions
--   keep it).
-- * The customer door gains customer.tools (the list) and customer.tool
--   (a start, its yes, its answer, a button on it), of no area: a
--   person's chat with the business is enough. Otherwise the body of
--   00462, unchanged.
-- +goose Up
ALTER TABLE whatsapp_public_workflows ADD COLUMN command text;
-- +goose StatementBegin
DO $$
DECLARE
  r record;
  base text;
  c text;
  n int;
BEGIN
  FOR r IN SELECT tenant_id, workflow_id, label FROM whatsapp_public_workflows WHERE command IS NULL ORDER BY tenant_id, created_at, label LOOP
    base := trim(both '-' from regexp_replace(lower(r.label), '[^a-z0-9]+', '-', 'g'));
    IF base !~ '^[a-z]' THEN
      base := trim(both '-' from 'menu-' || base);
    END IF;
    base := trim(both '-' from left(base, 21));
    IF length(base) < 2 THEN
      base := 'menu';
    END IF;
    c := base;
    n := 1;
    WHILE EXISTS (SELECT 1 FROM whatsapp_public_workflows WHERE tenant_id = r.tenant_id AND command = c)
       OR EXISTS (SELECT 1 FROM workflow_tools WHERE tenant_id = r.tenant_id AND environment = 'prod' AND command = c AND workflow_id <> r.workflow_id) LOOP
      n := n + 1;
      c := base || '-' || n;
    END LOOP;
    UPDATE whatsapp_public_workflows SET command = c WHERE tenant_id = r.tenant_id AND workflow_id = r.workflow_id;
  END LOOP;
END
$$;
-- +goose StatementEnd
ALTER TABLE whatsapp_public_workflows ALTER COLUMN command SET NOT NULL;
ALTER TABLE whatsapp_public_workflows ADD CONSTRAINT whatsapp_public_workflows_command_check CHECK (command ~ '^[a-z][a-z0-9-]{1,23}$');
ALTER TABLE whatsapp_public_workflows ADD CONSTRAINT whatsapp_public_workflows_command_key UNIQUE (tenant_id, command);

CREATE TABLE customer_tool_watches (
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  run_id           uuid PRIMARY KEY,
  phone_number_id  text NOT NULL,
  number           text NOT NULL CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  label            text NOT NULL,
  attempts         int NOT NULL DEFAULT 0,
  created_at       timestamptz NOT NULL DEFAULT now(),
  notified_at      timestamptz
);
CREATE INDEX customer_tool_watches_waiting ON customer_tool_watches (tenant_id) WHERE notified_at IS NULL;
ALTER TABLE customer_tool_watches ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_tool_watches FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_tool_watches TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_tool_watches TO taskiem_app;

-- The chat tick visits a business with a customer's answer waiting, even
-- when none of its members linked a chat app. The dispatch role reads
-- which businesses those are, and nothing else of them.
CREATE POLICY dispatch_waiting ON customer_tool_watches TO taskiem_dispatch USING (notified_at IS NULL);
GRANT SELECT (tenant_id, notified_at) ON customer_tool_watches TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_tool_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT w.tenant_id FROM customer_tool_watches w
    JOIN tenants t ON t.id = w.tenant_id AND t.status = 'active'
   WHERE w.notified_at IS NULL
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_customer_tool_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_customer_tool_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_customer_tool_tenants() TO taskiem_app;

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
    -- Customers' tools (made tools T5): workflows, of no area.
    WHEN 'customer.tools' THEN ARRAY[]::text[]
    WHEN 'customer.tool' THEN ARRAY[]::text[]
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
DROP FUNCTION taskiem_customer_tool_tenants();
DROP TABLE customer_tool_watches;
ALTER TABLE whatsapp_public_workflows DROP CONSTRAINT whatsapp_public_workflows_command_key;
ALTER TABLE whatsapp_public_workflows DROP CONSTRAINT whatsapp_public_workflows_command_check;
ALTER TABLE whatsapp_public_workflows DROP COLUMN command;
