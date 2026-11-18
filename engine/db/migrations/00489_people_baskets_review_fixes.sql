-- People chatting with people, stage 5b: ordering together, review fixes
-- (docs/spec/people-chatting.md "As built: stage 5b, ordering together",
-- "Review fixes"). 00487 and 00488 are other branches'.
--
-- Money:
-- - storefront_orders.paid_in_shares: an order placed together with "each
--   pays their share" is paid only by its shares' links (never by its own
--   full-price page, which would take the whole total again), and waits 24
--   hours for them (storefront.ShareTTL) instead of 30 minutes.
-- - storefront_order_share_links.refund_due_at: a share paid that its
--   order could not take (it lapsed, closed, or had less left) is not
--   marked paid but "refund due", seen by the member and the business.
-- - checked_at and checks: the payment check asks the provider about each
--   share's link by when it was last asked, backing off, and only while
--   its order is open (or closed within the hour).
--
-- Load and privacy:
-- - taskiem_baskets_changed and taskiem_baskets_moved: the live
--   connection's reads of the person's changed baskets and placed baskets
--   whose order moved, by the person's memberships, not reach per row.
-- - taskiem_basket_enter takes 'look': the shop read for a view, without an
--   audit entry (only adding items, placing and paying are audited).
-- - An index for placed baskets.
--
-- Consistency and row-level security:
-- - people_baskets.placing_at: when placing began (a second place is
--   refused meanwhile; a placing basket stuck for 10 minutes recovers).
-- - The guard keeps the holder's things (holder, state, how it is had and
--   paid, the order) to the holder; anyone else may only take it over
--   when the holder is gone.
-- - Shares are written while placing (and their card marked by the
--   placer); lines are added only to an open basket, never to a placed one.

-- +goose Up
ALTER TABLE storefront_orders ADD COLUMN paid_in_shares boolean NOT NULL DEFAULT false;
GRANT SELECT (paid_in_shares) ON storefront_orders TO taskiem_dispatch;

ALTER TABLE storefront_order_share_links
  ADD COLUMN refund_due_at timestamptz,
  ADD COLUMN checked_at    timestamptz,
  ADD COLUMN checks        int NOT NULL DEFAULT 0;
GRANT UPDATE (refund_due_at, checked_at, checks) ON storefront_order_share_links TO taskiem_app;
GRANT SELECT (refund_due_at, checked_at, checks) ON storefront_order_share_links TO taskiem_dispatch;
DROP INDEX storefront_order_share_links_due;
CREATE INDEX storefront_order_share_links_due ON storefront_order_share_links (tenant_id, checked_at NULLS FIRST)
  WHERE paid_at IS NULL AND refund_due_at IS NULL;
CREATE INDEX storefront_order_share_links_ref ON storefront_order_share_links (tenant_id, payment_ref);

ALTER TABLE people_baskets ADD COLUMN placing_at timestamptz;
GRANT SELECT (changed_at, placed_at) ON people_baskets TO taskiem_dispatch;
CREATE INDEX people_baskets_placed ON people_baskets (placed_at) WHERE state = 'placed';
CREATE INDEX people_baskets_placed_by ON people_baskets (placed_by) WHERE state = 'placed';

-- The status page: an order paid in shares says so (payment 'shares'),
-- so it offers no full-price Pay button.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_order_status(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, kind text, total_kobo bigint, payment text, payment_url text, storefront_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.status, o.kind, o.total_kobo, CASE WHEN o.paid_in_shares THEN 'shares' ELSE o.payment END, o.payment_url, o.storefront_id FROM storefront_orders o
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash
  UNION ALL
  SELECT 'processing', i.kind, 0, '', NULL, i.storefront_id FROM storefront_inbox i
   WHERE i.reference = p_reference AND i.status_hash = p_status_hash
     AND NOT EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference)
$$;
-- +goose StatementEnd

-- The payment check: tenants with orders waiting, or shares' links due a
-- check (unpaid, not refund due, their order open or closed within the
-- hour, last asked longer ago than their back-off).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.tenant_id FROM storefront_orders o WHERE o.status = 'awaiting_payment' OR o.pay_to_kobo IS NOT NULL AND o.payment_url IS NOT NULL
  UNION
  SELECT l.tenant_id FROM storefront_order_share_links l JOIN storefront_orders o ON o.id = l.order_id
   WHERE l.paid_at IS NULL AND l.refund_due_at IS NULL
     AND (o.status IN ('awaiting_payment', 'accepted', 'received', 'paid', 'dispatched', 'on_the_way', 'arrived') OR o.updated_at > now() - interval '1 hour')
     AND (l.checked_at IS NULL OR l.checked_at < now() - least(interval '30 seconds' * power(2, least(l.checks, 6)), interval '30 minutes'))
$$;
-- +goose StatementEnd

-- A placed basket's order as its members follow it: also which shares'
-- payments are to be refunded by the business.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_basket_order(p_basket uuid) RETURNS jsonb
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
                                WHERE l.order_id = o.id AND l.paid_at IS NOT NULL), '{}'::jsonb),
           'refunds', COALESCE((SELECT jsonb_object_agg(l.share_key::text, l.refund_due_at) FROM storefront_order_share_links l
                                WHERE l.order_id = o.id AND l.refund_due_at IS NOT NULL), '{}'::jsonb))
    INTO out
    FROM storefront_orders o WHERE o.tenant_id = v_business AND o.reference = v_reference AND o.person_id = v_placer;
  RETURN out;
END
$$;
-- +goose StatementEnd

-- The basket door: 'look' reads the shop for a view of the basket without
-- an audit entry; 'shop' (adding items, placing) and 'pay' are audited.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_basket_enter(p_basket uuid, p_what text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  p uuid := taskiem_person_scope();
BEGIN
  IF p IS NULL OR taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'the basket door is for the person signed in, with no other door open' USING ERRCODE = '42501';
  END IF;
  IF p_what NOT IN ('shop', 'pay', 'look') THEN
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
  IF p_what <> 'look' THEN
    PERFORM taskiem_audit_append(t, 'person', p::text, 'customer.basket', p::text,
      jsonb_build_object('what', p_what, 'door', 'basket', 'from', 'people'));
  END IF;
  PERFORM set_config('app.person_business', t::text, true);
  RETURN t;
END
$$;
-- +goose StatementEnd

-- The baskets the person in scope reaches, by their memberships (a group
-- they are in now, their own copy of a chat, a placed basket they placed
-- or have lines in), changed after p_since: the live connection's read.
-- +goose StatementBegin
CREATE FUNCTION taskiem_baskets_changed(p_since timestamptz, p_limit int)
RETURNS TABLE (id uuid, changed_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH me AS (SELECT taskiem_person_scope() AS p WHERE taskiem_person_scope() IS NOT NULL AND taskiem_person_business() IS NULL),
  mine AS (
    SELECT b.id FROM me, group_members m JOIN people_baskets b ON b.group_id = m.group_id
     WHERE m.person_id = me.p AND m.left_at IS NULL AND b.changed_at > p_since
    UNION
    SELECT b.id FROM me, people_chats c JOIN people_baskets b ON b.conversation_id = c.conversation_id
     WHERE c.person_id = me.p AND b.changed_at > p_since
    UNION
    SELECT b.id FROM me, people_baskets b WHERE b.state = 'placed' AND b.placed_by = me.p AND b.changed_at > p_since
    UNION
    SELECT b.id FROM me, people_basket_lines l JOIN people_baskets b ON b.id = l.basket_id
     WHERE l.person_id = me.p AND b.state = 'placed' AND b.changed_at > p_since)
  SELECT b.id, b.changed_at FROM people_baskets b WHERE b.id IN (SELECT mine.id FROM mine)
   ORDER BY b.changed_at, b.id LIMIT least(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd

-- The placed baskets (of the last 14 days) the person in scope reaches
-- whose order moved after p_since: its state, a payment, a share paid or
-- to be refunded, its delivery.
-- +goose StatementBegin
CREATE FUNCTION taskiem_baskets_moved(p_since timestamptz, p_limit int)
RETURNS TABLE (id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH me AS (SELECT taskiem_person_scope() AS p WHERE taskiem_person_scope() IS NOT NULL AND taskiem_person_business() IS NULL),
  mine AS (
    SELECT b.id FROM me, group_members m JOIN people_baskets b ON b.group_id = m.group_id
     WHERE m.person_id = me.p AND m.left_at IS NULL AND b.state = 'placed' AND b.placed_at > now() - interval '14 days'
    UNION
    SELECT b.id FROM me, people_chats c JOIN people_baskets b ON b.conversation_id = c.conversation_id
     WHERE c.person_id = me.p AND b.state = 'placed' AND b.placed_at > now() - interval '14 days'
    UNION
    SELECT b.id FROM me, people_baskets b WHERE b.state = 'placed' AND b.placed_by = me.p AND b.placed_at > now() - interval '14 days'
    UNION
    SELECT b.id FROM me, people_basket_lines l JOIN people_baskets b ON b.id = l.basket_id
     WHERE l.person_id = me.p AND b.state = 'placed' AND b.placed_at > now() - interval '14 days')
  SELECT b.id FROM people_baskets b
    JOIN storefront_orders o ON o.tenant_id = b.business_id AND o.reference = b.order_reference AND o.person_id = b.placed_by
   WHERE b.id IN (SELECT mine.id FROM mine)
     AND GREATEST(o.updated_at,
           (SELECT max(GREATEST(l.paid_at, l.refund_due_at)) FROM storefront_order_share_links l WHERE l.order_id = o.id),
           (SELECT max(GREATEST(d.accepted_at, d.picked_up_at, d.arrived_at, d.delivered_at)) FROM deliveries d
             WHERE d.tenant_id = o.tenant_id AND d.order_id = o.id)) > p_since
   ORDER BY b.placed_at DESC LIMIT least(GREATEST(p_limit, 1), 100)
$$;
-- +goose StatementEnd

-- What a basket is never changes; a placed basket stays placed; and the
-- holder's things are the holder's: anyone else changes only when it
-- changed (a line of theirs), or takes it over when the holder is gone
-- (no longer in the group, or the chat's other person gone), a placing
-- basket stuck for 10 minutes going back to closed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_basket_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  p uuid := taskiem_person_scope();
  gone boolean;
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
  IF p IS NOT NULL AND OLD.holder_id <> p AND (NEW.holder_id IS DISTINCT FROM OLD.holder_id OR NEW.state IS DISTINCT FROM OLD.state
     OR NEW.fulfilment IS DISTINCT FROM OLD.fulfilment OR NEW.zone IS DISTINCT FROM OLD.zone OR NEW.payment IS DISTINCT FROM OLD.payment
     OR NEW.order_reference IS DISTINCT FROM OLD.order_reference OR NEW.placed_by IS DISTINCT FROM OLD.placed_by
     OR NEW.placed_kobo IS DISTINCT FROM OLD.placed_kobo OR NEW.delivery_kobo IS DISTINCT FROM OLD.delivery_kobo
     OR NEW.closed_at IS DISTINCT FROM OLD.closed_at OR NEW.placed_at IS DISTINCT FROM OLD.placed_at) THEN
    IF OLD.group_id IS NOT NULL THEN
      gone := NOT EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = OLD.group_id AND m.person_id = OLD.holder_id AND m.left_at IS NULL);
    ELSE
      gone := NOT EXISTS (SELECT 1 FROM people_chats c WHERE c.person_id = p AND c.conversation_id = OLD.conversation_id
                            AND c.peer_id = OLD.holder_id AND taskiem_people_writable(c.peer_id));
    END IF;
    IF NOT gone OR NEW.holder_id <> p OR OLD.state NOT IN ('open', 'closed', 'placing')
       OR NOT (NEW.state = OLD.state OR (OLD.state = 'placing' AND NEW.state = 'closed' AND OLD.placing_at < now() - interval '10 minutes'))
       OR NEW.fulfilment IS DISTINCT FROM OLD.fulfilment OR NEW.zone IS DISTINCT FROM OLD.zone OR NEW.payment IS DISTINCT FROM OLD.payment
       OR NEW.order_reference IS DISTINCT FROM OLD.order_reference OR NEW.placed_by IS DISTINCT FROM OLD.placed_by
       OR NEW.placed_kobo IS DISTINCT FROM OLD.placed_kobo OR NEW.delivery_kobo IS DISTINCT FROM OLD.delivery_kobo
       OR NEW.placed_at IS DISTINCT FROM OLD.placed_at THEN
      RAISE EXCEPTION 'only the one holding the basket changes this' USING ERRCODE = '42501';
    END IF;
  END IF;
  IF NEW.state = 'placing' AND OLD.state IS DISTINCT FROM 'placing' THEN
    NEW.placing_at := clock_timestamp();
  END IF;
  NEW.changed_at := clock_timestamp();
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Lines: added only to an open basket; changed by their own person while
-- it is open, or by the holder until it is placed (the price placed at);
-- taken out until it is placed.
DROP POLICY own_insert ON people_basket_lines;
CREATE POLICY own_insert ON people_basket_lines FOR INSERT TO taskiem_app
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_basket_reach(basket_id)
    AND EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_lines.basket_id AND b.state = 'open'));
DROP POLICY own_update ON people_basket_lines;
CREATE POLICY own_update ON people_basket_lines FOR UPDATE TO taskiem_app
  USING (taskiem_basket_reach(basket_id) AND EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_lines.basket_id
    AND (b.state = 'open' AND people_basket_lines.person_id = taskiem_person_scope()
      OR b.state IN ('open', 'closed', 'placing') AND b.holder_id = taskiem_person_scope())))
  WITH CHECK (taskiem_basket_reach(basket_id));
DROP POLICY own_or_holder_delete ON people_basket_lines;
CREATE POLICY own_or_holder_delete ON people_basket_lines FOR DELETE TO taskiem_app
  USING (taskiem_basket_reach(basket_id) AND EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_lines.basket_id
    AND b.state <> 'placed' AND (people_basket_lines.person_id = taskiem_person_scope() OR b.holder_id = taskiem_person_scope())));

-- Shares: written by the holder while it is being placed; their card
-- marked by its placer once placed.
DROP POLICY reach_write ON people_basket_shares;
CREATE POLICY reach_write ON people_basket_shares FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_basket_reach(basket_id) AND EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_shares.basket_id
    AND b.state = 'placing' AND b.holder_id = taskiem_person_scope()));
DROP POLICY reach_update ON people_basket_shares;
CREATE POLICY reach_update ON people_basket_shares FOR UPDATE TO taskiem_app
  USING (taskiem_basket_reach(basket_id) AND EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_shares.basket_id
    AND (b.state = 'placing' AND b.holder_id = taskiem_person_scope() OR b.state = 'placed' AND b.placed_by = taskiem_person_scope())))
  WITH CHECK (taskiem_basket_reach(basket_id));

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_baskets_changed(timestamptz, int)', 'taskiem_baskets_moved(timestamptz, int)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP POLICY reach_update ON people_basket_shares;
CREATE POLICY reach_update ON people_basket_shares FOR UPDATE TO taskiem_app
  USING (taskiem_basket_reach(basket_id)) WITH CHECK (taskiem_basket_reach(basket_id));
DROP POLICY reach_write ON people_basket_shares;
CREATE POLICY reach_write ON people_basket_shares FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_basket_reach(basket_id));
DROP POLICY own_or_holder_delete ON people_basket_lines;
CREATE POLICY own_or_holder_delete ON people_basket_lines FOR DELETE TO taskiem_app
  USING (taskiem_basket_reach(basket_id) AND (person_id = taskiem_person_scope()
    OR EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_lines.basket_id AND b.holder_id = taskiem_person_scope())));
DROP POLICY own_update ON people_basket_lines;
CREATE POLICY own_update ON people_basket_lines FOR UPDATE TO taskiem_app
  USING (taskiem_basket_reach(basket_id) AND (person_id = taskiem_person_scope()
    OR EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_lines.basket_id AND b.holder_id = taskiem_person_scope())))
  WITH CHECK (taskiem_basket_reach(basket_id) AND (person_id = taskiem_person_scope()
    OR EXISTS (SELECT 1 FROM people_baskets b WHERE b.id = people_basket_lines.basket_id AND b.holder_id = taskiem_person_scope())));
DROP POLICY own_insert ON people_basket_lines;
CREATE POLICY own_insert ON people_basket_lines FOR INSERT TO taskiem_app
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_basket_reach(basket_id));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_basket_guard() RETURNS trigger
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

DROP FUNCTION taskiem_baskets_moved(timestamptz, int);
DROP FUNCTION taskiem_baskets_changed(timestamptz, int);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_basket_enter(p_basket uuid, p_what text) RETURNS uuid
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_basket_order(p_basket uuid) RETURNS jsonb
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.tenant_id FROM storefront_orders o WHERE o.status = 'awaiting_payment' OR o.pay_to_kobo IS NOT NULL AND o.payment_url IS NOT NULL
  UNION
  SELECT l.tenant_id FROM storefront_order_share_links l WHERE l.paid_at IS NULL AND l.created_at > now() - interval '7 days'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_order_status(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, kind text, total_kobo bigint, payment text, payment_url text, storefront_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.status, o.kind, o.total_kobo, o.payment, o.payment_url, o.storefront_id FROM storefront_orders o
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash
  UNION ALL
  SELECT 'processing', i.kind, 0, '', NULL, i.storefront_id FROM storefront_inbox i
   WHERE i.reference = p_reference AND i.status_hash = p_status_hash
     AND NOT EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference)
$$;
-- +goose StatementEnd

DROP INDEX people_baskets_placed_by;
DROP INDEX people_baskets_placed;
REVOKE SELECT (changed_at, placed_at) ON people_baskets FROM taskiem_dispatch;
ALTER TABLE people_baskets DROP COLUMN placing_at;
DROP INDEX storefront_order_share_links_ref;
DROP INDEX storefront_order_share_links_due;
CREATE INDEX storefront_order_share_links_due ON storefront_order_share_links (tenant_id) WHERE paid_at IS NULL;
REVOKE SELECT (refund_due_at, checked_at, checks) ON storefront_order_share_links FROM taskiem_dispatch;
REVOKE UPDATE (refund_due_at, checked_at, checks) ON storefront_order_share_links FROM taskiem_app;
ALTER TABLE storefront_order_share_links DROP COLUMN refund_due_at, DROP COLUMN checked_at, DROP COLUMN checks;
REVOKE SELECT (paid_in_shares) ON storefront_orders FROM taskiem_dispatch;
ALTER TABLE storefront_orders DROP COLUMN paid_in_shares;
