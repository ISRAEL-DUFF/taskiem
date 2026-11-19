-- People chatting with people, stage 5b: ordering together
-- (docs/spec/people-chatting.md §10.3, §11 A and C, §17 Q8;
-- docs/spec/people-chatting-5b.md C, with the founder's answers C1, C2,
-- C3; decision 0069 points 8 and 9). "As built: stage 5b, ordering
-- together". (00485 is another branch's.)
--
-- A shared basket belongs to one group or one chat between two people,
-- and to one business. Each member adds their own lines from the
-- business's shop; the one holding it (the starter, or whoever they
-- handed it to: C1) closes it, may reopen it until it is placed, and
-- places it through the door as their own order. The business sees one
-- order from that person, with a note of whose lines are whose, and never
-- the group or the chat: nothing here is in the business's scope but the
-- pay links of members paying their own share (C2), which name a share by
-- an opaque key, never a person.
--
-- Who sees a basket is row-level security (taskiem_basket_reach): the
-- members of its group now, the two people of its chat (each by their own
-- copy), and, once placed, whoever placed it or has lines in it (they owe
-- their share after leaving). A business's door open reaches nothing.
-- +goose Up

CREATE TABLE people_baskets (
  id               uuid PRIMARY KEY,
  business_id      uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  -- The business's name when the basket was started (its card's).
  business_name    text NOT NULL CHECK (length(business_name) <= 200),
  -- Where it was started: a group, or a chat between two people (the
  -- pair's conversation id, which each person's own copy names).
  group_id         uuid REFERENCES people_groups(id) ON DELETE CASCADE,
  conversation_id  uuid,
  CHECK ((group_id IS NULL) <> (conversation_id IS NULL)),
  starter_id       uuid NOT NULL REFERENCES users(id),
  -- Who closes it and places the order (C1): the starter until they hand
  -- it on.
  holder_id        uuid NOT NULL REFERENCES users(id),
  state            text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed', 'placing', 'placed', 'cancelled')),
  fulfilment       text CHECK (fulfilment IN ('pickup', 'delivery')),
  zone             text CHECK (length(zone) <= 120),
  -- How it is paid (C2): the placer pays all, or each pays their share.
  payment          text CHECK (payment IN ('all', 'each')),
  order_reference  text CHECK (length(order_reference) <= 80),
  placed_by        uuid REFERENCES users(id),
  placed_kobo      bigint CHECK (placed_kobo >= 0),
  delivery_kobo    bigint CHECK (delivery_kobo >= 0),
  CHECK (state <> 'placed' OR (order_reference IS NOT NULL AND placed_by IS NOT NULL AND payment IS NOT NULL)),
  created_at       timestamptz NOT NULL DEFAULT now(),
  closed_at        timestamptz,
  placed_at        timestamptz,
  changed_at       timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX people_baskets_group ON people_baskets (group_id) WHERE group_id IS NOT NULL;
CREATE INDEX people_baskets_conversation ON people_baskets (conversation_id) WHERE conversation_id IS NOT NULL;
CREATE INDEX people_baskets_changed ON people_baskets (changed_at);

-- Each member's own lines: an item (or one of a product's sizes and
-- colours) and how many, with its name and price when added (the basket
-- shows the shop's price now beside it).
CREATE TABLE people_basket_lines (
  id          uuid PRIMARY KEY,
  basket_id   uuid NOT NULL REFERENCES people_baskets(id) ON DELETE CASCADE,
  person_id   uuid NOT NULL REFERENCES users(id),
  item_id     text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100),
  quantity    int NOT NULL CHECK (quantity BETWEEN 1 AND 99),
  name        text NOT NULL CHECK (length(name) <= 200),
  price_kobo  bigint NOT NULL CHECK (price_kobo >= 0),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX people_basket_lines_basket ON people_basket_lines (basket_id);
CREATE INDEX people_basket_lines_person ON people_basket_lines (person_id);

-- Each member's share, made when the order is placed (C2): their items
-- and an equal part of the delivery; key names it in the business's pay
-- links (storefront_order_share_links) without naming the person.
CREATE TABLE people_basket_shares (
  basket_id      uuid NOT NULL REFERENCES people_baskets(id) ON DELETE CASCADE,
  person_id      uuid NOT NULL REFERENCES users(id),
  key            uuid NOT NULL UNIQUE,
  items_kobo     bigint NOT NULL CHECK (items_kobo >= 0),
  delivery_kobo  bigint NOT NULL CHECK (delivery_kobo >= 0),
  amount_kobo    bigint NOT NULL CHECK (amount_kobo >= 0),
  -- "I'll pay all": when the placer sent this member a "Your share" card.
  card_at        timestamptz,
  PRIMARY KEY (basket_id, person_id)
);

-- A line changing changes its basket (the live connection reads it).
-- +goose StatementBegin
CREATE FUNCTION taskiem_basket_touch() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  UPDATE people_baskets SET changed_at = clock_timestamp() WHERE id = COALESCE(NEW.basket_id, OLD.basket_id);
  RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER people_basket_lines_touch AFTER INSERT OR UPDATE OR DELETE ON people_basket_lines
  FOR EACH ROW EXECUTE FUNCTION taskiem_basket_touch();
CREATE TRIGGER people_basket_shares_touch AFTER INSERT OR UPDATE ON people_basket_shares
  FOR EACH ROW EXECUTE FUNCTION taskiem_basket_touch();

-- What a basket is never changes: its business, where it is, its starter.
-- +goose StatementBegin
CREATE FUNCTION taskiem_basket_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.business_id IS DISTINCT FROM OLD.business_id OR NEW.group_id IS DISTINCT FROM OLD.group_id
     OR NEW.conversation_id IS DISTINCT FROM OLD.conversation_id OR NEW.starter_id IS DISTINCT FROM OLD.starter_id
     OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'a basket''s business, place and starter are kept' USING ERRCODE = '42501';
  END IF;
  IF OLD.state = 'placed' AND (NEW.state <> 'placed' OR NEW.order_reference IS DISTINCT FROM OLD.order_reference
     OR NEW.placed_by IS DISTINCT FROM OLD.placed_by OR NEW.payment IS DISTINCT FROM OLD.payment) THEN
    RAISE EXCEPTION 'a placed basket stays placed' USING ERRCODE = '42501';
  END IF;
  NEW.changed_at := clock_timestamp();
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER people_baskets_guard BEFORE UPDATE ON people_baskets
  FOR EACH ROW EXECUTE FUNCTION taskiem_basket_guard();

-- A line stays its person's, in its basket, of its item (the holder sets
-- the price it was placed at).
-- +goose StatementBegin
CREATE FUNCTION taskiem_basket_line_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.basket_id IS DISTINCT FROM OLD.basket_id OR NEW.person_id IS DISTINCT FROM OLD.person_id OR NEW.item_id IS DISTINCT FROM OLD.item_id THEN
    RAISE EXCEPTION 'a basket line''s person and item are kept' USING ERRCODE = '42501';
  END IF;
  NEW.updated_at := now();
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER people_basket_lines_guard BEFORE UPDATE ON people_basket_lines
  FOR EACH ROW EXECUTE FUNCTION taskiem_basket_line_guard();

-- The business's side: a pay link for one share of an order (C2), paid
-- by the member or by the placer for them. share_key is the basket
-- share's key: the business sees parts of its order paid, never by whom.
CREATE TABLE storefront_order_share_links (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  order_id     uuid NOT NULL REFERENCES storefront_orders(id) ON DELETE CASCADE,
  share_key    uuid NOT NULL,
  amount_kobo  bigint NOT NULL CHECK (amount_kobo >= 100),
  payment_url  text NOT NULL,
  payment_ref  text NOT NULL,
  paid_at      timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (order_id, share_key)
);
CREATE INDEX storefront_order_share_links_due ON storefront_order_share_links (tenant_id) WHERE paid_at IS NULL;
ALTER TABLE storefront_order_share_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_order_share_links FORCE ROW LEVEL SECURITY;
REVOKE ALL ON storefront_order_share_links FROM PUBLIC;
CREATE POLICY tenant_isolation ON storefront_order_share_links TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON storefront_order_share_links TO taskiem_app;
GRANT UPDATE (paid_at) ON storefront_order_share_links TO taskiem_app;
CREATE POLICY dispatch_read ON storefront_order_share_links FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, order_id, share_key, paid_at, created_at) ON storefront_order_share_links TO taskiem_dispatch;

-- A basket card in a chat or a group.
ALTER TABLE people_messages DROP CONSTRAINT people_messages_card_type_check,
  ADD CONSTRAINT people_messages_card_type_check CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split', 'basket'));
ALTER TABLE group_messages DROP CONSTRAINT group_messages_card_type_check,
  ADD CONSTRAINT group_messages_card_type_check CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split', 'basket'));

-- --- reach ---

-- Whether the person in scope (no business in scope) may see the basket:
-- a member of its group now; one of its chat's two people, by their own
-- copy (a block made later leaves it shared, as A8 leaves group messages;
-- a card sent across a block is never delivered, so its basket is never
-- found); once placed, whoever placed it or has lines in it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_basket_reach(p_basket uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT taskiem_person_scope() IS NOT NULL AND taskiem_person_business() IS NULL AND EXISTS (
    SELECT 1 FROM people_baskets b WHERE b.id = p_basket AND (
      (b.group_id IS NOT NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = b.group_id
         AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL))
      OR (b.conversation_id IS NOT NULL AND EXISTS (SELECT 1 FROM people_chats c WHERE c.conversation_id = b.conversation_id
         AND c.person_id = taskiem_person_scope()))
      OR (b.state = 'placed' AND (b.placed_by = taskiem_person_scope()
         OR EXISTS (SELECT 1 FROM people_basket_lines l WHERE l.basket_id = b.id AND l.person_id = taskiem_person_scope())))))
$$;
-- +goose StatementEnd

-- The basket door: the person in scope, reaching the basket, goes into
-- its business for one thing (its shop, or paying a share), as the card
-- door does: an active business showing itself that has not blocked them;
-- audited in its chain, without the basket, group or chat.
-- +goose StatementBegin
CREATE FUNCTION taskiem_basket_enter(p_basket uuid, p_what text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  p uuid := taskiem_person_scope();
BEGIN
  IF p IS NULL OR taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'the basket door is for the person signed in, with no other door open' USING ERRCODE = '42501';
  END IF;
  IF p_what NOT IN ('shop', 'pay') THEN
    RAISE EXCEPTION 'the basket door has no %', p_what USING ERRCODE = '22023';
  END IF;
  IF NOT taskiem_basket_reach(p_basket) THEN
    RAISE EXCEPTION 'no such basket' USING ERRCODE = 'P0002';
  END IF;
  SELECT business_id INTO t FROM people_baskets WHERE id = p_basket;
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = t AND status = 'active' AND kind = 'business')
     OR NOT EXISTS (SELECT 1 FROM users WHERE id = p AND status = 'active')
     OR NOT (EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = t AND s.status = 'published') OR taskiem_discover_open(t)) THEN
    RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
  END IF;
  IF EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = t AND c.person_id = p AND c.line = 'app' AND c.blocked_at IS NOT NULL) THEN
    RAISE EXCEPTION 'the business does not take messages from this customer' USING ERRCODE = '42501', HINT = 'blocked';
  END IF;
  PERFORM set_config('app.tenant_scope', '{' || t::text || '}', true);
  PERFORM taskiem_audit_append(t, 'person', p::text, 'customer.basket', p::text,
    jsonb_build_object('what', p_what, 'door', 'basket', 'from', 'people'));
  PERFORM set_config('app.person_business', t::text, true);
  RETURN t;
END
$$;
-- +goose StatementEnd

-- A placed basket's order as its members follow it (C2, "after placing"):
-- its state, what it costs and what was paid, its delivery's progress, and
-- which shares' pay links were paid; for someone reaching the basket only,
-- never the business's customer details.
-- +goose StatementBegin
CREATE FUNCTION taskiem_basket_order(p_basket uuid) RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_business uuid;
  v_reference text;
  v_placer uuid;
  out jsonb;
BEGIN
  IF NOT taskiem_basket_reach(p_basket) THEN
    RETURN NULL;
  END IF;
  SELECT business_id, order_reference, placed_by INTO v_business, v_reference, v_placer FROM people_baskets WHERE id = p_basket AND state = 'placed';
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  SELECT jsonb_build_object('status', o.status, 'payment', o.payment, 'total_kobo', o.total_kobo, 'due_kobo', o.total_kobo + o.extra_kobo,
           'paid_kobo', o.paid_kobo, 'expires_at', o.expires_at,
           'delivery', (SELECT jsonb_build_object('status', d.status, 'accepted_at', d.accepted_at, 'picked_up_at', d.picked_up_at,
                               'arrived_at', d.arrived_at, 'delivered_at', d.delivered_at)
                          FROM deliveries d WHERE d.tenant_id = o.tenant_id AND d.order_id = o.id
                           AND d.status IN ('accepted', 'picked_up', 'arrived', 'delivered') LIMIT 1),
           'shares', COALESCE((SELECT jsonb_object_agg(l.share_key::text, l.paid_at) FROM storefront_order_share_links l
                                WHERE l.order_id = o.id AND l.paid_at IS NOT NULL), '{}'::jsonb))
    INTO out
    FROM storefront_orders o WHERE o.tenant_id = v_business AND o.reference = v_reference AND o.person_id = v_placer;
  RETURN out;
END
$$;
-- +goose StatementEnd

-- The payment checker also looks at shares' pay links waiting.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.tenant_id FROM storefront_orders o WHERE o.status = 'awaiting_payment' OR o.pay_to_kobo IS NOT NULL AND o.payment_url IS NOT NULL
  UNION
  SELECT l.tenant_id FROM storefront_order_share_links l WHERE l.paid_at IS NULL AND l.created_at > now() - interval '7 days'
$$;
-- +goose StatementEnd

-- --- the policies ---

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['people_baskets', 'people_basket_lines', 'people_basket_shares'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- The definers read only what reach and the order's follow need.
CREATE POLICY dispatch_read ON people_baskets FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, business_id, group_id, conversation_id, state, order_reference, placed_by) ON people_baskets TO taskiem_dispatch;
CREATE POLICY dispatch_read ON people_basket_lines FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (basket_id, person_id) ON people_basket_lines TO taskiem_dispatch;

-- A basket: seen by whoever reaches it; started by a member of the group
-- now, or one of the chat's two people, as themself and its holder;
-- changed by whoever reaches it (the API decides who may do what: the
-- holder closes, reopens, hands on and places); never deleted but with
-- its group.
CREATE POLICY reach_read ON people_baskets FOR SELECT TO taskiem_app
  USING (taskiem_basket_reach(id));
CREATE POLICY starter_insert ON people_baskets FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND starter_id = taskiem_person_scope() AND holder_id = starter_id AND state = 'open'
    AND ((group_id IS NOT NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = people_baskets.group_id
            AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL))
      OR (conversation_id IS NOT NULL AND EXISTS (SELECT 1 FROM people_chats c WHERE c.conversation_id = people_baskets.conversation_id
            AND c.person_id = taskiem_person_scope()))));
CREATE POLICY reach_update ON people_baskets FOR UPDATE TO taskiem_app
  USING (taskiem_basket_reach(id))
  WITH CHECK (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL);
GRANT SELECT, INSERT, UPDATE ON people_baskets TO taskiem_app;

-- Lines: seen by whoever reaches the basket; written by their own person;
-- deleted by their own person, or by the basket's holder.
CREATE POLICY reach_read ON people_basket_lines FOR SELECT TO taskiem_app
  USING (taskiem_basket_reach(basket_id));
CREATE POLICY own_insert ON people_basket_lines FOR INSERT TO taskiem_app
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_basket_reach(basket_id));
CREATE POLICY own_update ON people_basket_lines FOR UPDATE TO taskiem_app
  USING (taskiem_basket_reach(basket_id) AND (person_id = taskiem_person_scope()
    OR EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_lines.basket_id AND b.holder_id = taskiem_person_scope())))
  WITH CHECK (taskiem_basket_reach(basket_id) AND (person_id = taskiem_person_scope()
    OR EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_lines.basket_id AND b.holder_id = taskiem_person_scope())));
CREATE POLICY own_or_holder_delete ON people_basket_lines FOR DELETE TO taskiem_app
  USING (taskiem_basket_reach(basket_id) AND (person_id = taskiem_person_scope()
    OR EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_lines.basket_id AND b.holder_id = taskiem_person_scope())));
GRANT SELECT, INSERT, UPDATE, DELETE ON people_basket_lines TO taskiem_app;

-- Shares: seen by whoever reaches the basket; written when it is placed.
CREATE POLICY reach_read ON people_basket_shares FOR SELECT TO taskiem_app
  USING (taskiem_basket_reach(basket_id));
CREATE POLICY reach_write ON people_basket_shares FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_basket_reach(basket_id));
CREATE POLICY reach_update ON people_basket_shares FOR UPDATE TO taskiem_app
  USING (taskiem_basket_reach(basket_id)) WITH CHECK (taskiem_basket_reach(basket_id));
GRANT SELECT, INSERT, UPDATE ON people_basket_shares TO taskiem_app;

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_basket_reach(uuid)', 'taskiem_basket_enter(uuid, text)', 'taskiem_basket_order(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_basket_touch() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION taskiem_basket_guard() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION taskiem_basket_line_guard() FROM PUBLIC;

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT o.tenant_id FROM storefront_orders o WHERE o.status = 'awaiting_payment' OR o.pay_to_kobo IS NOT NULL AND o.payment_url IS NOT NULL
$$;
-- +goose StatementEnd
DROP TABLE people_basket_shares;
DROP TABLE people_basket_lines;
DROP TABLE people_baskets CASCADE;
DROP FUNCTION taskiem_basket_order(uuid);
DROP FUNCTION taskiem_basket_enter(uuid, text);
DROP FUNCTION taskiem_basket_reach(uuid);
DROP FUNCTION taskiem_basket_guard();
DROP FUNCTION taskiem_basket_line_guard();
DROP FUNCTION taskiem_basket_touch();
DROP TABLE storefront_order_share_links;
DELETE FROM people_messages WHERE card_type = 'basket';
DELETE FROM group_messages WHERE card_type = 'basket';
ALTER TABLE people_messages DROP CONSTRAINT people_messages_card_type_check,
  ADD CONSTRAINT people_messages_card_type_check CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split'));
ALTER TABLE group_messages DROP CONSTRAINT group_messages_card_type_check,
  ADD CONSTRAINT group_messages_card_type_check CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split'));
