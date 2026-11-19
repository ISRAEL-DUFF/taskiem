-- One customer across the app and a business's own WhatsApp number
-- (docs/spec/taskiem-for-everyone.md §11, §5.5 item 4, §14; stage 4).
--
-- Inside one business, a person in the app whose phone is verified on
-- their account (user_phones) and a customer thread on the business's own
-- WhatsApp number that WhatsApp has delivered a message from are one
-- customer. The business's own key makes the subject of the person's phone
-- and finds the thread: nothing is looked up across businesses.
--
--   * The link is kept on the WhatsApp thread: linked_to names the app
--     thread of the same customer (one WhatsApp thread per app thread).
--     The business sees one thread with both lines; the app thread's id
--     stands for the pair.
--   * A phone change unlinks (§5.5 item 4): the WhatsApp thread stays under
--     the old number and says the customer changed their number
--     (number_changed_at); the app thread is matched again only by a
--     message WhatsApp delivers after the change (relink_after).
--   * The person's copy shows the WhatsApp line's messages too, marked:
--     person_messages.line, and person_conversations.whatsapp_since (when
--     the line was joined; NULL while there is none).
--   * The business's chat link offers Chat on WhatsApp beside Chat on
--     Taskiem when it has its own number, unless it turns that off
--     (customer_chat_link_settings.link_whatsapp).
--
-- The way back (taskiem_customer_reach) gains two actions: customer.link
-- (joining or parting the lines, audited) and customer.whatsapp (a
-- WhatsApp message shown in the person's copy, audited; refused after the
-- person's Block, as a message is). Three narrow definers find, inside the
-- business in scope only, a person chatting with it by their verified
-- phone, that person's verified phone, and the app threads not yet looked
-- at for a pair (the one-time look for existing pairs).
-- +goose Up

ALTER TABLE customer_conversations ADD COLUMN linked_to uuid REFERENCES customer_conversations(id) ON DELETE SET NULL;
ALTER TABLE customer_conversations ADD COLUMN linked_at timestamptz;
ALTER TABLE customer_conversations ADD CONSTRAINT customer_conversations_linked_line CHECK (linked_to IS NULL OR line = 'whatsapp');
-- The app thread: matched again only by a WhatsApp message after this.
ALTER TABLE customer_conversations ADD COLUMN relink_after timestamptz;
-- The WhatsApp thread: its customer's app line moved to another number.
ALTER TABLE customer_conversations ADD COLUMN number_changed_at timestamptz;
-- The app thread was looked at for a WhatsApp thread to join.
ALTER TABLE customer_conversations ADD COLUMN link_checked_at timestamptz;
GRANT SELECT (link_checked_at) ON customer_conversations TO taskiem_dispatch;

ALTER TABLE person_messages ADD COLUMN line text NOT NULL DEFAULT 'app' CHECK (line IN ('app', 'whatsapp'));
ALTER TABLE person_conversations ADD COLUMN whatsapp_since timestamptz;

ALTER TABLE customer_chat_link_settings ADD COLUMN link_whatsapp boolean NOT NULL DEFAULT true;

-- The way back gains customer.link and customer.whatsapp.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_reach(p_tenant uuid, p_person uuid, p_action text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_stopped timestamptz;
  v_found boolean;
BEGIN
  -- A person's own transaction never reaches a person: after the door it
  -- has the business in scope, and would open someone else's copy.
  IF taskiem_person_scope() IS NOT NULL
     OR (taskiem_person_business() IS NOT NULL AND taskiem_person_reached() IS NULL) THEN
    RAISE EXCEPTION 'the way back is for the business''s own transaction' USING ERRCODE = '42501';
  END IF;
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  IF p_action NOT IN ('customer.message', 'customer.read', 'customer.block', 'customer.typing', 'customer.link', 'customer.whatsapp') THEN
    RAISE EXCEPTION 'the customer door has no action %', p_action USING ERRCODE = '22023';
  END IF;
  SELECT true, person_blocked_at INTO v_found, v_stopped FROM customer_conversations
   WHERE tenant_id = p_tenant AND person_id = p_person AND line = 'app';
  IF NOT COALESCE(v_found, false) THEN
    RAISE EXCEPTION 'no conversation with that customer in the app' USING ERRCODE = 'P0002';
  END IF;
  IF v_stopped IS NOT NULL AND p_action IN ('customer.message', 'customer.whatsapp') THEN
    RAISE EXCEPTION 'this customer has stopped messages' USING ERRCODE = '42501', HINT = 'stopped';
  END IF;
  IF p_action <> 'customer.read' AND p_action <> 'customer.typing' THEN
    PERFORM taskiem_audit_append(p_tenant, COALESCE(p_detail ->> 'actor_type', 'system'), COALESCE(p_detail ->> 'actor', 'system'), p_action,
      p_person::text, (COALESCE(p_detail, '{}') - 'actor' - 'actor_type') || jsonb_build_object('door', 'customer'));
  END IF;
  PERFORM set_config('app.person_reach', p_person::text, true);
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd

-- The person chatting in the app with the business in scope whose
-- verified phone is p_number, and since when it is theirs; nothing for a
-- number that is no such person's. Never from a person's own transaction
-- (it would tell a person who else chats with the business).
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_link_person(p_tenant uuid, p_number text)
RETURNS TABLE (person_id uuid, verified_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF taskiem_person_scope() IS NOT NULL OR NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'the business''s own transaction only' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT p.user_id, p.verified_at FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active'
   WHERE p.number = p_number
     AND EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = p_tenant AND c.line = 'app' AND c.person_id = p.user_id);
END
$$;
-- +goose StatementEnd

-- The verified phone of a person chatting in the app with the business in
-- scope, and since when it is theirs. A person's own transaction asks
-- only for themself.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_link_phone(p_tenant uuid, p_person uuid)
RETURNS TABLE (number text, verified_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope()))
     OR (taskiem_person_scope() IS NOT NULL AND taskiem_person_scope() <> p_person) THEN
    RAISE EXCEPTION 'the business''s own transaction, or the person''s own' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT p.number, p.verified_at FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active'
   WHERE p.user_id = p_person
     AND EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = p_tenant AND c.line = 'app' AND c.person_id = p_person);
END
$$;
-- +goose StatementEnd

-- App threads not yet looked at for a WhatsApp thread to join (every
-- thread there was when this shipped, then each new one), ids only, the
-- oldest first.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_link_due(p_limit int)
RETURNS TABLE (tenant_id uuid, conversation_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.id FROM customer_conversations c JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active'
   WHERE c.line = 'app' AND c.link_checked_at IS NULL AND c.person_id IS NOT NULL
   ORDER BY c.id
   LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_customer_link_person(uuid,text)', 'taskiem_customer_link_phone(uuid,uuid)', 'taskiem_customer_link_due(integer)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_customer_link_due(integer);
DROP FUNCTION taskiem_customer_link_phone(uuid, uuid);
DROP FUNCTION taskiem_customer_link_person(uuid, text);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_reach(p_tenant uuid, p_person uuid, p_action text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_stopped timestamptz;
  v_found boolean;
BEGIN
  IF taskiem_person_scope() IS NOT NULL
     OR (taskiem_person_business() IS NOT NULL AND taskiem_person_reached() IS NULL) THEN
    RAISE EXCEPTION 'the way back is for the business''s own transaction' USING ERRCODE = '42501';
  END IF;
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
  PERFORM set_config('app.person_reach', p_person::text, true);
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd
ALTER TABLE customer_chat_link_settings DROP COLUMN link_whatsapp;
ALTER TABLE person_conversations DROP COLUMN whatsapp_since;
ALTER TABLE person_messages DROP COLUMN line;
REVOKE SELECT (link_checked_at) ON customer_conversations FROM taskiem_dispatch;
ALTER TABLE customer_conversations DROP COLUMN link_checked_at;
ALTER TABLE customer_conversations DROP COLUMN number_changed_at;
ALTER TABLE customer_conversations DROP COLUMN relink_after;
ALTER TABLE customer_conversations DROP CONSTRAINT customer_conversations_linked_line;
ALTER TABLE customer_conversations DROP COLUMN linked_at;
ALTER TABLE customer_conversations DROP COLUMN linked_to;
