-- The customer door, after review (docs/spec/taskiem-for-everyone.md §8,
-- §14, §15; "As built: stage 2, review fixes (conversations)").
--
--  * The way back (taskiem_customer_reach) no longer sets app.person_scope,
--    which also opened the users row (users.person_self): it sets its own
--    setting, app.person_reach (taskiem_person_reached()), which only the
--    person's copy of their conversations with that one business reads.
--    It refuses a person's own transaction (a door entered there would
--    let a person reach another person's copy).
--  * Typing is refused while either side has blocked the other, and never
--    marked or read for a business from a person's transaction.
--  * App threads are a person's own: keyed by (tenant, person) alone; the
--    old unique key holds WhatsApp threads only, so a phone that changes
--    hands never moves one person's thread to another.
--  * A customer waiting is kept as waiting_since (the first unanswered
--    message), and the sweep that tells owners skips a thread until it
--    may be due (wait_check_at), oldest first.
--  * The person's copy knows whether the business wrote first
--    (by_business: not one of the person's new chats) and whether the
--    offers question was shown (offers_asked_at).
--  * The business's record of an offers answer outlives the account
--    (person_id set null).
--  * Retention's purge answers the files of the messages it deletes, so
--    their objects are deleted from the business's storage too.
-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION taskiem_person_reached() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE AS $$
  SELECT NULLIF(current_setting('app.person_reach', true), '')::uuid
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION taskiem_person_reached() TO taskiem_app, taskiem_dispatch;

-- The person's copy: their own in their own transaction (one business's
-- once a door names it), or one person's with one business once that
-- business reached them. The users row stays keyed on the person's own
-- scope only.
ALTER POLICY person_own ON person_conversations
  USING ((person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
      OR (person_id = taskiem_person_reached() AND tenant_id = taskiem_person_business()))
  WITH CHECK ((person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
      OR (person_id = taskiem_person_reached() AND tenant_id = taskiem_person_business()));
ALTER POLICY person_own ON person_messages
  USING ((person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
      OR (person_id = taskiem_person_reached() AND tenant_id = taskiem_person_business()))
  WITH CHECK ((person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
      OR (person_id = taskiem_person_reached() AND tenant_id = taskiem_person_business()));
ALTER POLICY person_own ON person_files
  USING ((person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
      OR (person_id = taskiem_person_reached() AND tenant_id = taskiem_person_business()))
  WITH CHECK ((person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
      OR (person_id = taskiem_person_reached() AND tenant_id = taskiem_person_business()));

-- App threads by person; the old key for WhatsApp threads only.
ALTER TABLE customer_conversations DROP CONSTRAINT customer_conversations_line_key;
CREATE UNIQUE INDEX customer_conversations_whatsapp_key ON customer_conversations (tenant_id, phone_number_id, subject_id) WHERE line = 'whatsapp';
-- The first customer message no one has answered (§8.6): set by a
-- customer's message when unset, cleared by the business's own answer
-- (not Taskiem's closed reply, nor an offer).
ALTER TABLE customer_conversations ADD COLUMN waiting_since timestamptz;
-- The owners' sweep looks at a waiting thread again only from this time:
-- a business closed now cannot be due before it has been open long enough.
ALTER TABLE customer_conversations ADD COLUMN wait_check_at timestamptz;
UPDATE customer_conversations SET waiting_since = last_inbound_at
 WHERE line = 'app' AND last_inbound_at IS NOT NULL AND (last_reply_at IS NULL OR last_reply_at < last_inbound_at);
CREATE INDEX customer_conversations_waiting_since ON customer_conversations (waiting_since) WHERE line = 'app' AND waiting_since IS NOT NULL;
GRANT SELECT (waiting_since, wait_check_at) ON customer_conversations TO taskiem_dispatch;

-- The person's copy: a chat the business opened by writing first is not
-- one of the person's new chats (§15); the offers question, once shown.
ALTER TABLE person_conversations ADD COLUMN by_business boolean NOT NULL DEFAULT false;
ALTER TABLE person_conversations ADD COLUMN offers_asked_at timestamptz;
UPDATE person_conversations SET offers_asked_at = COALESCE(offers_at, created_at) WHERE offers <> 'unasked';

-- The business keeps its record of an answer after the account is gone.
ALTER TABLE customer_offer_consents DROP CONSTRAINT customer_offer_consents_pkey;
ALTER TABLE customer_offer_consents ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE customer_offer_consents ADD CONSTRAINT customer_offer_consents_pkey PRIMARY KEY (id);
ALTER TABLE customer_offer_consents ADD CONSTRAINT customer_offer_consents_person UNIQUE (tenant_id, person_id);
ALTER TABLE customer_offer_consents ALTER COLUMN person_id DROP NOT NULL;
ALTER TABLE customer_offer_consents DROP CONSTRAINT customer_offer_consents_person_id_fkey;
ALTER TABLE customer_offer_consents ADD CONSTRAINT customer_offer_consents_person_id_fkey FOREIGN KEY (person_id) REFERENCES users(id) ON DELETE SET NULL;

-- The way back: from the business's own transaction only.
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

-- Typing: one's own side, never while either side blocked the other.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_typing(p_tenant uuid, p_person uuid, p_side text) RETURNS void
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_found boolean;
  v_blocked timestamptz;
  v_stopped timestamptz;
BEGIN
  IF (p_side = 'person' AND p_person IS DISTINCT FROM taskiem_person_scope())
     OR (p_side = 'business' AND (NOT (p_tenant = ANY (taskiem_tenant_scope())) OR taskiem_person_scope() IS NOT NULL))
     OR p_side NOT IN ('person', 'business') THEN
    RAISE EXCEPTION 'typing is for one''s own side' USING ERRCODE = '42501';
  END IF;
  SELECT true, blocked_at, person_blocked_at INTO v_found, v_blocked, v_stopped FROM customer_conversations
   WHERE tenant_id = p_tenant AND person_id = p_person AND line = 'app';
  IF NOT COALESCE(v_found, false) THEN
    RETURN;
  END IF;
  IF v_blocked IS NOT NULL THEN
    RAISE EXCEPTION 'the business does not take messages from this customer' USING ERRCODE = '42501', HINT = 'blocked';
  END IF;
  IF v_stopped IS NOT NULL THEN
    RAISE EXCEPTION 'this customer has stopped messages' USING ERRCODE = '42501', HINT = 'stopped';
  END IF;
  INSERT INTO customer_typing (tenant_id, person_id, side, until) VALUES (p_tenant, p_person, p_side, now() + interval '6 seconds')
    ON CONFLICT (tenant_id, person_id, side) DO UPDATE SET until = EXCLUDED.until;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_typing_of(p_tenant uuid, p_person uuid, p_side text) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT ((p_side = 'business' AND p_person IS NOT DISTINCT FROM taskiem_person_scope())
     OR (p_side = 'person' AND p_tenant = ANY (taskiem_tenant_scope()) AND taskiem_person_scope() IS NULL)) THEN
    RETURN false;
  END IF;
  IF EXISTS (SELECT 1 FROM customer_conversations WHERE tenant_id = p_tenant AND person_id = p_person AND line = 'app'
              AND (blocked_at IS NOT NULL OR person_blocked_at IS NOT NULL)) THEN
    RETURN false;
  END IF;
  RETURN EXISTS (SELECT 1 FROM customer_typing WHERE tenant_id = p_tenant AND person_id = p_person AND side = p_side AND until > now());
END
$$;
-- +goose StatementEnd

-- Customers waiting (§8.6): app threads with a message no one answered
-- since waiting_since, past p_after by the clock (the caller counts open
-- hours), the owner not told of this wait, not blocked either way, not
-- skipped until later; the oldest first.
DROP FUNCTION taskiem_customer_waiting(interval);
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_waiting(p_after interval)
RETURNS TABLE (tenant_id uuid, conversation_id uuid, waiting_since timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.id, c.waiting_since FROM customer_conversations c JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active'
   WHERE c.line = 'app' AND c.waiting_since IS NOT NULL AND c.waiting_since < now() - p_after
     AND c.waiting_since > now() - interval '7 days'
     AND (c.owner_told_at IS NULL OR c.owner_told_at < c.waiting_since)
     AND (c.wait_check_at IS NULL OR c.wait_check_at <= now())
     AND c.blocked_at IS NULL AND c.person_blocked_at IS NULL
   ORDER BY c.waiting_since
   LIMIT 500
$$;
-- +goose StatementEnd

-- Retention's purge answers each deleted message's file in the business's
-- storage, so its object is deleted too.
GRANT SELECT (tenant_id, attachment) ON customer_messages TO taskiem_dispatch;
DROP FUNCTION taskiem_customer_messages_purge();
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_messages_purge()
RETURNS TABLE (tenant_id uuid, path text) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM order_drafts o WHERE o.expires_at <= now();
  RETURN QUERY WITH gone AS (
    DELETE FROM customer_messages m WHERE m.expires_at <= now() RETURNING m.tenant_id, m.attachment ->> 'path' AS path
  ) SELECT g.tenant_id, g.path FROM gone g WHERE g.path IS NOT NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_customer_waiting(interval)', 'taskiem_customer_messages_purge()'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_customer_messages_purge();
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_messages_purge()
RETURNS bigint LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
  d bigint;
BEGIN
  DELETE FROM order_drafts o WHERE o.expires_at <= now();
  GET DIAGNOSTICS d = ROW_COUNT;
  DELETE FROM customer_messages m WHERE m.expires_at <= now();
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n + d;
END
$$;
-- +goose StatementEnd
REVOKE SELECT (tenant_id, attachment) ON customer_messages FROM taskiem_dispatch;
DROP FUNCTION taskiem_customer_waiting(interval);
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
  FOREACH f IN ARRAY ARRAY['taskiem_customer_waiting(interval)', 'taskiem_customer_messages_purge()'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_typing_of(p_tenant uuid, p_person uuid, p_side text) RETURNS boolean
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_typing(p_tenant uuid, p_person uuid, p_side text) RETURNS void
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_reach(p_tenant uuid, p_person uuid, p_action text, p_detail jsonb)
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
-- The answers of people whose accounts are gone go with them, as before.
DELETE FROM customer_offer_consents WHERE person_id IS NULL;
ALTER TABLE customer_offer_consents DROP CONSTRAINT customer_offer_consents_person_id_fkey;
ALTER TABLE customer_offer_consents ADD CONSTRAINT customer_offer_consents_person_id_fkey FOREIGN KEY (person_id) REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE customer_offer_consents ALTER COLUMN person_id SET NOT NULL;
ALTER TABLE customer_offer_consents DROP CONSTRAINT customer_offer_consents_person;
ALTER TABLE customer_offer_consents DROP CONSTRAINT customer_offer_consents_pkey;
ALTER TABLE customer_offer_consents DROP COLUMN id;
ALTER TABLE customer_offer_consents ADD CONSTRAINT customer_offer_consents_pkey PRIMARY KEY (tenant_id, person_id);
ALTER TABLE person_conversations DROP COLUMN offers_asked_at;
ALTER TABLE person_conversations DROP COLUMN by_business;
REVOKE SELECT (waiting_since, wait_check_at) ON customer_conversations FROM taskiem_dispatch;
DROP INDEX customer_conversations_waiting_since;
ALTER TABLE customer_conversations DROP COLUMN wait_check_at;
ALTER TABLE customer_conversations DROP COLUMN waiting_since;
DROP INDEX customer_conversations_whatsapp_key;
-- App threads that came to share a subject (a phone that changed hands)
-- keep the newest under the old key; the others' subject is marked.
UPDATE customer_conversations c SET subject_id = left(c.subject_id, 150) || ':' || c.id::text
 WHERE c.line = 'app' AND EXISTS (SELECT 1 FROM customer_conversations d WHERE d.tenant_id = c.tenant_id AND d.line = 'app'
   AND d.subject_id = c.subject_id AND d.created_at > c.created_at);
ALTER TABLE customer_conversations ADD CONSTRAINT customer_conversations_line_key UNIQUE (tenant_id, line, phone_number_id, subject_id);
ALTER POLICY person_own ON person_files
  USING (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
  WITH CHECK (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()));
ALTER POLICY person_own ON person_messages
  USING (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
  WITH CHECK (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()));
ALTER POLICY person_own ON person_conversations
  USING (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()))
  WITH CHECK (person_id = taskiem_person_scope() AND (taskiem_person_business() IS NULL OR tenant_id = taskiem_person_business()));
DROP FUNCTION taskiem_person_reached();
