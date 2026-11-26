-- Editing a sent message (docs/spec/chat-previews-and-editing.md §3).
--
-- * Every chat's messages say when they were last edited (edited_at) and
--   how many times (edits), which a client sends back with its edit so an
--   edit made on another device is not overwritten (409 stale).
-- * acted_at: Taskiem made something from the message's words (an order
--   read from a customer's message, a menu answered, a "@Taskiem"
--   answered), so its words are kept as they were.
-- * link_message_edits: the earlier wordings of a business-to-business
--   message, for the sending business's audit log only, never shown in the
--   chat. The other business never had them, so they are kept on the
--   sender's side alone, under the same row-level security as its
--   messages, as long as the message is kept.
-- * The business's way back into a customer's copy (taskiem_customer_reach)
--   takes customer_chat.message.edit, audited in the business's chain.
-- +goose Up
ALTER TABLE link_messages ADD COLUMN edited_at timestamptz, ADD COLUMN edits int NOT NULL DEFAULT 0 CHECK (edits >= 0), ADD COLUMN acted_at timestamptz;
ALTER TABLE people_messages ADD COLUMN edited_at timestamptz, ADD COLUMN edits int NOT NULL DEFAULT 0 CHECK (edits >= 0);
ALTER TABLE group_messages ADD COLUMN edited_at timestamptz, ADD COLUMN edits int NOT NULL DEFAULT 0 CHECK (edits >= 0);
ALTER TABLE customer_messages ADD COLUMN edited_at timestamptz, ADD COLUMN edits int NOT NULL DEFAULT 0 CHECK (edits >= 0), ADD COLUMN acted_at timestamptz;
ALTER TABLE person_messages ADD COLUMN edited_at timestamptz, ADD COLUMN edits int NOT NULL DEFAULT 0 CHECK (edits >= 0);

CREATE TABLE link_message_edits (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  -- This side's own copy of the message, and the message (the same on both).
  message_id      uuid NOT NULL REFERENCES link_messages(id) ON DELETE CASCADE,
  pair_id         uuid NOT NULL,
  -- The member who edited it (their actor id).
  edited_by       text NOT NULL CHECK (length(edited_by) <= 200),
  edited_at       timestamptz NOT NULL DEFAULT now(),
  -- What it said before: its words, or a file's caption.
  words_before    text CHECK (length(words_before) <= 4000),
  caption_before  text CHECK (length(caption_before) <= 4000)
);
CREATE INDEX link_message_edits_message ON link_message_edits (tenant_id, pair_id, edited_at);
ALTER TABLE link_message_edits ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_message_edits FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_message_edits TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON link_message_edits TO taskiem_app;

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
  IF p_action NOT IN ('customer.message', 'customer.read', 'customer.block', 'customer.typing', 'customer.link', 'customer.whatsapp',
      'customer_chat.message.edit') THEN
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

-- +goose Down
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
DROP TABLE link_message_edits;
ALTER TABLE person_messages DROP COLUMN edits, DROP COLUMN edited_at;
ALTER TABLE customer_messages DROP COLUMN acted_at, DROP COLUMN edits, DROP COLUMN edited_at;
ALTER TABLE group_messages DROP COLUMN edits, DROP COLUMN edited_at;
ALTER TABLE people_messages DROP COLUMN edits, DROP COLUMN edited_at;
ALTER TABLE link_messages DROP COLUMN acted_at, DROP COLUMN edits, DROP COLUMN edited_at;
