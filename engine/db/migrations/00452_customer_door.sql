-- Customers chatting with businesses in the app (docs/spec/taskiem-for-
-- everyone.md §8, §14, §15, stage 2, stream A; decision 0068).
--
-- A customer conversation in the app has two copies, as a conversation
-- between linked businesses does (00294):
--   * the business's: its customer thread (customer_conversations, now of
--     a line, 'whatsapp' or 'app'), its messages sealed under the
--     customer's subject in the business (the phone's), as today;
--   * the person's: person_conversations and person_messages, under the
--     person's own row-level scope (taskiem_person_scope(), 00448).
-- A message is the same id on both copies (customer_messages.id =
-- person_messages.id). Each side keeps, and its retention deletes, its own.
--
-- The one crossing from a person into a business is taskiem_customer_enter:
-- for one person and one business, for one of a fixed list of actions
-- (§8.4), refused when the business blocked the person or turned the
-- needed area off, audited in the business's chain, the scope then set to
-- that business for the rest of the transaction. The crossing back, a
-- business writing to the person's copy, is taskiem_customer_reach: only
-- for a person with an app conversation with the business in scope,
-- audited, the scope then set to that person and that business only.
-- +goose Up

-- What a person's transaction may see of their own conversations once a
-- door has named one business: that business's only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_business() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE AS $$
  SELECT NULLIF(current_setting('app.person_business', true), '')::uuid
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION taskiem_person_business() TO taskiem_app, taskiem_dispatch;

-- The business's copy: a thread is on a line. An app thread is the
-- person's (person_id), one per person per business; its
-- phone_number_id is 'app'. The person stopping messages (Block) and the
-- business blocking them are kept on the thread; nothing is deleted by
-- either (§8.3).
ALTER TABLE customer_conversations ADD COLUMN line text NOT NULL DEFAULT 'whatsapp' CHECK (line IN ('whatsapp', 'app'));
ALTER TABLE customer_conversations ADD COLUMN person_id uuid REFERENCES users(id) ON DELETE SET NULL;
-- The customer blocked the business ("This customer has stopped messages").
ALTER TABLE customer_conversations ADD COLUMN person_blocked_at timestamptz;
-- The business blocked the customer: the door refuses them.
ALTER TABLE customer_conversations ADD COLUMN blocked_at timestamptz;
-- The business's last message in the thread (an app thread's answer).
ALTER TABLE customer_conversations ADD COLUMN last_reply_at timestamptz;
-- The closed reply, once a day (§8.6), and the owner told of a customer
-- waiting two hours, once per wait.
ALTER TABLE customer_conversations ADD COLUMN closed_reply_at timestamptz;
ALTER TABLE customer_conversations ADD COLUMN owner_told_at timestamptz;
ALTER TABLE customer_conversations DROP CONSTRAINT customer_conversations_tenant_id_phone_number_id_subject_id_key;
ALTER TABLE customer_conversations ADD CONSTRAINT customer_conversations_line_key UNIQUE (tenant_id, line, phone_number_id, subject_id);
ALTER TABLE customer_conversations ADD CONSTRAINT customer_conversations_app_number CHECK (line = 'whatsapp' OR phone_number_id = 'app');
CREATE UNIQUE INDEX customer_conversations_person ON customer_conversations (tenant_id, person_id) WHERE line = 'app';
CREATE INDEX customer_conversations_waiting ON customer_conversations (last_inbound_at) WHERE line = 'app';
-- The doors read only whether a person has a thread, and its blocks; the
-- waiting sweep only what it needs to find a customer waiting.
CREATE POLICY dispatch_read ON customer_conversations FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, line, person_id, person_blocked_at, blocked_at, last_inbound_at, last_reply_at, owner_told_at) ON customer_conversations TO taskiem_dispatch;

-- A message's kind gains card (an order, a pay link, a booking offer, a
-- product); an app message carries its card, its attachment (in the
-- business's own storage) and whether it is an offer (§15).
ALTER TABLE customer_messages DROP CONSTRAINT customer_messages_kind_check;
ALTER TABLE customer_messages ADD CONSTRAINT customer_messages_kind_check CHECK (kind IN ('text', 'media', 'card'));
ALTER TABLE customer_messages ADD COLUMN card jsonb CHECK (card IS NULL OR jsonb_typeof(card) = 'object');
ALTER TABLE customer_messages ADD COLUMN attachment jsonb CHECK (attachment IS NULL OR jsonb_typeof(attachment) = 'object');
ALTER TABLE customer_messages ADD COLUMN offer boolean NOT NULL DEFAULT false;
CREATE INDEX customer_messages_offers ON customer_messages (tenant_id, conversation_id, created_at) WHERE offer;

-- The person's copy (§14): kept until they delete the chat or the account.
CREATE TABLE person_conversations (
  id               uuid PRIMARY KEY,
  person_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  muted            boolean NOT NULL DEFAULT false,
  archived         boolean NOT NULL DEFAULT false,
  -- The person blocked the business; the business blocked them (mirrored).
  blocked_at       timestamptz,
  business_blocked_at timestamptz,
  -- Their answer to "Get offers from {business}?", asked once (§15); the
  -- business keeps its own record (customer_offer_consents).
  offers           text NOT NULL DEFAULT 'unasked' CHECK (offers IN ('unasked', 'yes', 'no')),
  offers_at        timestamptz,
  last_message_at  timestamptz NOT NULL DEFAULT now(),
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (person_id, tenant_id)
);
CREATE INDEX person_conversations_list ON person_conversations (person_id, last_message_at DESC);
ALTER TABLE person_conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_conversations FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON person_conversations TO taskiem_app
  USING (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
  WITH CHECK (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()));
GRANT SELECT, INSERT, UPDATE, DELETE ON person_conversations TO taskiem_app;

CREATE TABLE person_messages (
  -- The message: the same id as the business's copy.
  id               uuid PRIMARY KEY,
  person_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  conversation_id  uuid NOT NULL REFERENCES person_conversations(id) ON DELETE CASCADE,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  -- The person sent it.
  mine             boolean NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('text', 'media', 'card')),
  body             text NOT NULL DEFAULT '' CHECK (length(body) <= 4000),
  -- Who wrote it on the business's side: a member's first name, or the
  -- business's own name for what Taskiem says for it.
  sender_name      text NOT NULL DEFAULT '' CHECK (length(sender_name) <= 120),
  card             jsonb CHECK (card IS NULL OR jsonb_typeof(card) = 'object'),
  -- {kind: photo|file|voice, type, size, name, file}: the person's own
  -- copy (person_files).
  attachment       jsonb CHECK (attachment IS NULL OR jsonb_typeof(attachment) = 'object'),
  offer            boolean NOT NULL DEFAULT false,
  client_id        text CHECK (length(client_id) <= 80),
  -- On the business's message, when the person read it; on the person's
  -- own, when the business did (mirrored).
  read_at          timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'card') = (card IS NOT NULL))
);
CREATE INDEX person_messages_stream ON person_messages (person_id, conversation_id, created_at DESC);
CREATE INDEX person_messages_sent ON person_messages (person_id, created_at) WHERE mine;
CREATE UNIQUE INDEX person_messages_client ON person_messages (person_id, conversation_id, client_id) WHERE client_id IS NOT NULL;
ALTER TABLE person_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON person_messages TO taskiem_app
  USING (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
  WITH CHECK (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()));
GRANT SELECT, INSERT, UPDATE, DELETE ON person_messages TO taskiem_app;

-- The person's copy of a photo, voice note or file (§8.5): their own,
-- apart from the business's (which is in the business's storage).
CREATE TABLE person_files (
  id          uuid PRIMARY KEY,
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  message_id  uuid NOT NULL REFERENCES person_messages(id) ON DELETE CASCADE,
  type        text NOT NULL CHECK (length(type) <= 100),
  size        bigint NOT NULL CHECK (size > 0 AND size <= 26214400),
  name        text NOT NULL DEFAULT '' CHECK (length(name) <= 200),
  data        bytea NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX person_files_message ON person_files (person_id, message_id);
ALTER TABLE person_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_files FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON person_files TO taskiem_app
  USING (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
  WITH CHECK (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()));
GRANT SELECT, INSERT, DELETE ON person_files TO taskiem_app;

-- The business's record of a customer's answer to offers, and when (§15,
-- §24 point 1). Only yes receives offers.
CREATE TABLE customer_offer_consents (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  person_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  answer       boolean NOT NULL,
  answered_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, person_id)
);
ALTER TABLE customer_offer_consents ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_offer_consents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_offer_consents TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON customer_offer_consents TO taskiem_app;

-- A business's answers when nobody answers (§8.6, §24 point 4): the closed
-- reply (on by default, its words the business's own or Taskiem's), Needs
-- you after 30 minutes, and the owner told after 2 hours.
CREATE TABLE customer_chat_settings (
  tenant_id     uuid PRIMARY KEY REFERENCES tenants(id),
  closed_reply  boolean NOT NULL DEFAULT true,
  closed_words  text CHECK (length(closed_words) BETWEEN 1 AND 300),
  needs_you     boolean NOT NULL DEFAULT true,
  tell_owner    boolean NOT NULL DEFAULT true,
  updated_by    text NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE customer_chat_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_chat_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_chat_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON customer_chat_settings TO taskiem_app;

-- Reports to Taskiem's moderators (§15): the last 20 messages of the
-- person's copy, sent with their consent. The person writes one and reads
-- none back; moderators read them.
CREATE TABLE customer_reports (
  id          uuid PRIMARY KEY,
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  reason      text NOT NULL CHECK (length(reason) BETWEEN 1 AND 40),
  note        text NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
  consent     boolean NOT NULL CHECK (consent),
  messages    jsonb NOT NULL CHECK (jsonb_typeof(messages) = 'array'),
  status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'reviewed')),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_reports_open ON customer_reports (created_at) WHERE status = 'open';
ALTER TABLE customer_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_reports FORCE ROW LEVEL SECURITY;
CREATE POLICY person_writes ON customer_reports FOR INSERT TO taskiem_app WITH CHECK (person_id = taskiem_person_scope());
GRANT INSERT ON customer_reports TO taskiem_app;

-- Who is typing, for the other side's typing line: a moment's state, no
-- words, not audited, as link_typing. No one reads the table directly.
CREATE TABLE customer_typing (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  person_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  side       text NOT NULL CHECK (side IN ('person', 'business')),
  until      timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, person_id, side)
);
ALTER TABLE customer_typing ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_typing FORCE ROW LEVEL SECURITY;
CREATE POLICY no_app ON customer_typing TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch_all ON customer_typing TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON customer_typing FROM PUBLIC;
GRANT SELECT (tenant_id, person_id, side, until), INSERT, UPDATE ON customer_typing TO taskiem_dispatch;

-- Storefront orders gain the kind app (an order placed in the app, §8.4)
-- and the person who placed it (an app order, or a booking made in the app).
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class', 'quote', 'trade', 'chat', 'app'));
ALTER TABLE storefront_orders ADD COLUMN person_id uuid REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX storefront_orders_person ON storefront_orders (tenant_id, person_id, created_at DESC) WHERE person_id IS NOT NULL;
GRANT SELECT (person_id) ON storefront_orders TO taskiem_dispatch;

-- The door (§8.4). p_person must be the transaction's own person; the
-- business active and a business (not a workflows workspace); the action
-- one of the list; the person not blocked by the business (Block itself
-- excepted, which is the person's own); an app thread with the business,
-- unless opening one (customer.start: its published shop, its chat link,
-- or an order the person placed with it). The area the action needs on.
-- Audited in the business's chain; then the scope is the business, and
-- the person's own rows are that business's only, for the rest of the
-- transaction.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_enter(p_person uuid, p_tenant uuid, p_action text, p_detail jsonb)
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

-- The crossing back: the business in scope writes to the person's copy
-- of their app conversation (a reply, a card, a read mark, a block). The
-- person's own Block refuses messages (customer.message), never a read
-- mark or the business's own block. Audited in the business's chain; then
-- the person's rows are reachable, that business's only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_reach(p_tenant uuid, p_person uuid, p_action text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_stopped timestamptz;
  v_found boolean;
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  IF p_action NOT IN ('customer.message', 'customer.read', 'customer.block', 'customer.typing') THEN
    RAISE EXCEPTION 'the customer door has no action %', p_action USING ERRCODE = '22023';
  END IF;
  SELECT true, person_blocked_at INTO v_found, v_stopped FROM customer_conversations
   WHERE tenant_id = p_tenant AND person_id = p_person AND line = 'app';
  IF NOT COALESCE(v_found, false) THEN
    RAISE EXCEPTION 'no conversation with that customer in the app' USING ERRCODE = 'P0002';
  END IF;
  IF v_stopped IS NOT NULL AND p_action = 'customer.message' THEN
    RAISE EXCEPTION 'this customer has stopped messages' USING ERRCODE = '42501', HINT = 'stopped';
  END IF;
  IF p_action <> 'customer.read' AND p_action <> 'customer.typing' THEN
    PERFORM taskiem_audit_append(p_tenant, COALESCE(p_detail ->> 'actor_type', 'system'), COALESCE(p_detail ->> 'actor', 'system'), p_action,
      p_person::text, (COALESCE(p_detail, '{}') - 'actor' - 'actor_type') || jsonb_build_object('door', 'customer'));
  END IF;
  PERFORM set_config('app.person_scope', p_person::text, true);
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd

-- The names of the businesses a person has app conversations with, for
-- their chat list: a business's name only, and only where they chat.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_business_names(p_tenants uuid[])
RETURNS TABLE (tenant_id uuid, name text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, t.name FROM tenants t
   WHERE t.id = ANY (p_tenants) AND taskiem_person_scope() IS NOT NULL
     AND EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = t.id AND c.person_id = taskiem_person_scope() AND c.line = 'app')
$$;
-- +goose StatementEnd

-- Typing: the person in scope, or the business in scope, for six seconds.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_typing(p_tenant uuid, p_person uuid, p_side text) RETURNS void
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF (p_side = 'person' AND p_person IS DISTINCT FROM taskiem_person_scope())
     OR (p_side = 'business' AND NOT (p_tenant = ANY (taskiem_tenant_scope())))
     OR p_side NOT IN ('person', 'business') THEN
    RAISE EXCEPTION 'typing is for one''s own side' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM customer_conversations WHERE tenant_id = p_tenant AND person_id = p_person AND line = 'app') THEN
    RETURN;
  END IF;
  INSERT INTO customer_typing (tenant_id, person_id, side, until) VALUES (p_tenant, p_person, p_side, now() + interval '6 seconds')
    ON CONFLICT (tenant_id, person_id, side) DO UPDATE SET until = EXCLUDED.until;
END
$$;
-- +goose StatementEnd
-- Whether the other side is typing: asked by the person (of the business)
-- or by the business (of the person).
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_typing_of(p_tenant uuid, p_person uuid, p_side text) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT ((p_side = 'business' AND p_person IS NOT DISTINCT FROM taskiem_person_scope())
     OR (p_side = 'person' AND p_tenant = ANY (taskiem_tenant_scope()))) THEN
    RETURN false;
  END IF;
  RETURN EXISTS (SELECT 1 FROM customer_typing WHERE tenant_id = p_tenant AND person_id = p_person AND side = p_side AND until > now());
END
$$;
-- +goose StatementEnd

-- An order placed in the app (§8.4), called through the door (the
-- business in scope): the business's built-in order action, enabled, its
-- front enabled, its storefront published and taking orders. Writes the
-- sealed submission for the order worker, as a page's order is written;
-- a repeated idempotency token returns the first reference; NULL when
-- the business takes no orders now; a reference taken raises 23505.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_order(p_action uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  sf uuid;
  ref text;
BEGIN
  IF taskiem_person_scope() IS NULL OR taskiem_person_business() IS DISTINCT FROM v_me THEN
    RAISE EXCEPTION 'an order in the app goes through the customer door' USING ERRCODE = '42501';
  END IF;
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem AND tenant_id = v_me;
  IF ref IS NOT NULL THEN
    RETURN ref;
  END IF;
  IF EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference)
     OR EXISTS (SELECT 1 FROM storefront_inbox i WHERE i.reference = p_reference) THEN
    RAISE EXCEPTION 'reference taken' USING ERRCODE = '23505', CONSTRAINT = 'storefront_inbox_reference_key';
  END IF;
  SELECT s.id INTO sf FROM storefront_actions a
    JOIN storefront_fronts f ON f.id = a.front_id AND f.enabled
    JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published' AND s.orders_paused IS NULL
   WHERE a.id = p_action AND a.enabled AND a.kind = 'order' AND a.builtin AND s.tenant_id = v_me;
  IF sf IS NULL THEN
    RETURN NULL;
  END IF;
  INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload, action_id)
    VALUES (gen_random_uuid(), sf, v_me, 'order', p_reference, p_idem, p_status_hash, p_payload, p_action)
    ON CONFLICT (idem) DO NOTHING;
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem;
  RETURN ref;
END
$$;
-- +goose StatementEnd

-- Customers waiting (§8.6): app threads whose customer wrote last, past
-- p_after, the owner not yet told of this wait. Ids only, across
-- businesses, for the sweep that tells each business's owner.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_waiting(p_after interval)
RETURNS TABLE (tenant_id uuid, conversation_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.id FROM customer_conversations c JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active'
   WHERE c.line = 'app' AND c.last_inbound_at IS NOT NULL AND c.last_inbound_at < now() - p_after
     AND c.last_inbound_at > now() - interval '3 days'
     AND (c.last_reply_at IS NULL OR c.last_reply_at < c.last_inbound_at)
     AND (c.owner_told_at IS NULL OR c.owner_told_at < c.last_inbound_at)
     AND c.blocked_at IS NULL
   LIMIT 500
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_customer_enter(uuid, uuid, text, jsonb)', 'taskiem_customer_reach(uuid, uuid, text, jsonb)',
      'taskiem_customer_business_names(uuid[])', 'taskiem_customer_typing(uuid, uuid, text)', 'taskiem_customer_typing_of(uuid, uuid, text)',
      'taskiem_customer_order(uuid, text, bytea, bytea, bytea)', 'taskiem_customer_waiting(interval)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_customer_waiting(interval);
DROP FUNCTION taskiem_customer_order(uuid, text, bytea, bytea, bytea);
DROP FUNCTION taskiem_customer_typing_of(uuid, uuid, text);
DROP FUNCTION taskiem_customer_typing(uuid, uuid, text);
DROP FUNCTION taskiem_customer_business_names(uuid[]);
DROP FUNCTION taskiem_customer_reach(uuid, uuid, text, jsonb);
DROP FUNCTION taskiem_customer_enter(uuid, uuid, text, jsonb);
REVOKE SELECT (person_id) ON storefront_orders FROM taskiem_dispatch;
DROP INDEX storefront_orders_person;
ALTER TABLE storefront_orders DROP COLUMN person_id;
UPDATE storefront_orders SET kind = 'order' WHERE kind = 'app';
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking', 'hire', 'space', 'tickets', 'class', 'quote', 'trade', 'chat'));
DROP TABLE customer_typing;
DROP TABLE customer_reports;
DROP TABLE customer_chat_settings;
DROP TABLE customer_offer_consents;
DROP TABLE person_files;
DROP TABLE person_messages;
DROP TABLE person_conversations;
DROP INDEX customer_messages_offers;
ALTER TABLE customer_messages DROP COLUMN offer;
ALTER TABLE customer_messages DROP COLUMN attachment;
ALTER TABLE customer_messages DROP COLUMN card;
DELETE FROM customer_messages WHERE kind = 'card' OR conversation_id IN (SELECT id FROM customer_conversations WHERE line = 'app');
DELETE FROM order_drafts WHERE conversation_id IN (SELECT id FROM customer_conversations WHERE line = 'app');
ALTER TABLE customer_messages DROP CONSTRAINT customer_messages_kind_check;
ALTER TABLE customer_messages ADD CONSTRAINT customer_messages_kind_check CHECK (kind IN ('text', 'media'));
REVOKE SELECT (id, tenant_id, line, person_id, person_blocked_at, blocked_at, last_inbound_at, last_reply_at, owner_told_at) ON customer_conversations FROM taskiem_dispatch;
DROP POLICY dispatch_read ON customer_conversations;
DELETE FROM customer_conversations WHERE line = 'app';
DROP INDEX customer_conversations_waiting;
DROP INDEX customer_conversations_person;
ALTER TABLE customer_conversations DROP CONSTRAINT customer_conversations_app_number;
ALTER TABLE customer_conversations DROP CONSTRAINT customer_conversations_line_key;
ALTER TABLE customer_conversations ADD CONSTRAINT customer_conversations_tenant_id_phone_number_id_subject_id_key UNIQUE (tenant_id, phone_number_id, subject_id);
ALTER TABLE customer_conversations DROP COLUMN owner_told_at;
ALTER TABLE customer_conversations DROP COLUMN closed_reply_at;
ALTER TABLE customer_conversations DROP COLUMN last_reply_at;
ALTER TABLE customer_conversations DROP COLUMN blocked_at;
ALTER TABLE customer_conversations DROP COLUMN person_blocked_at;
ALTER TABLE customer_conversations DROP COLUMN person_id;
ALTER TABLE customer_conversations DROP COLUMN line;
DROP FUNCTION taskiem_person_business();
