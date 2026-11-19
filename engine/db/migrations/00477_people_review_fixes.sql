-- People chatting with people, stage 5a: the review's fixes
-- (docs/spec/people-chatting.md, "As built: stage 5a, the two streams
-- joined", "Review fixes"; decision 0069).
--
-- * people_chats.peer_wrote_at: when the other person first wrote into
--   this copy. Whether a sender is still a stranger to the recipient (the
--   checks on strangers' messages, §8.4) is read from it, not from the
--   messages of the sender's own copy, which "delete for me" empties.
-- * people_chats.empty: a copy opened (a person's link, a number) that no
--   one has written in yet. The lists leave it out until the first
--   message, so opening someone's link adds no empty row at the top.
-- * people_messages.screen gains 'transfer': a share card naming an
--   account, from someone the recipient has not added, warns as a
--   transfer does (warn_transfer) as well as warn.
-- * taskiem_people_reach tells a suspended account (hint 'suspended')
--   from a deleted one (hint 'gone'); taskiem_people_names answers a
--   suspended account's name with suspended, and gone only once deleted.
-- * taskiem_people_match follows the matched person's "find me by my
--   number" towards the asker as well as "find me from contacts":
--   Nobody matches no one, My contacts only people they added.
-- * taskiem_customer_business_photos: the photos (logos) of the
--   businesses a person has app conversations with, for You's one Chats
--   list, by taskiem_business_photo's one rule.
-- +goose Up

ALTER TABLE people_chats ADD COLUMN peer_wrote_at timestamptz;
ALTER TABLE people_chats ADD COLUMN empty boolean NOT NULL DEFAULT false;
UPDATE people_chats c SET peer_wrote_at = (SELECT min(m.created_at) FROM people_messages m WHERE m.person_id = c.person_id AND m.chat_id = c.id AND NOT m.mine)
 WHERE EXISTS (SELECT 1 FROM people_messages m WHERE m.person_id = c.person_id AND m.chat_id = c.id AND NOT m.mine);
UPDATE people_chats c SET empty = true
 WHERE c.started_by_me AND c.hidden_at IS NULL AND c.waiting_at IS NULL AND c.accepted_at IS NULL AND c.peer_wrote_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM people_messages m WHERE m.person_id = c.person_id AND m.chat_id = c.id);
CREATE INDEX people_chats_page ON people_chats (person_id, last_message_at DESC, id DESC) WHERE hidden_at IS NULL AND NOT empty;

ALTER TABLE people_messages DROP CONSTRAINT people_messages_screen_check;
ALTER TABLE people_messages ADD CONSTRAINT people_messages_screen_check CHECK (screen IN ('warn', 'transfer'));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_reach(p_chat uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_peer uuid;
  v_conv uuid;
  v_deleted boolean;
  v_active boolean;
BEGIN
  IF taskiem_person_scope() IS NULL THEN
    RAISE EXCEPTION 'a people chat is reached from a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  IF taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'a business is in scope' USING ERRCODE = '42501';
  END IF;
  SELECT c.peer_id, c.conversation_id INTO v_peer, v_conv FROM people_chats c
   WHERE c.id = p_chat AND c.person_id = taskiem_person_scope();
  IF NOT FOUND OR v_peer IS NULL THEN
    RAISE EXCEPTION 'no such chat' USING ERRCODE = 'P0002';
  END IF;
  SELECT p.deleted, p.active INTO v_deleted, v_active FROM person_profiles p WHERE p.person_id = v_peer;
  IF NOT FOUND OR v_deleted THEN
    RAISE EXCEPTION 'the account was deleted' USING ERRCODE = 'P0002', HINT = 'gone';
  END IF;
  IF NOT v_active THEN
    RAISE EXCEPTION 'the account is suspended' USING ERRCODE = 'P0002', HINT = 'suspended';
  END IF;
  PERFORM set_config('app.people_reach', v_conv::text, true);
  PERFORM set_config('app.people_peer', v_peer::text, true);
  RETURN v_peer;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_people_names(uuid[]);
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_names(p_ids uuid[])
RETURNS TABLE (person_id uuid, name text, gone boolean, suspended boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF taskiem_person_scope() IS NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT p.person_id, CASE WHEN NOT p.deleted THEN p.name ELSE '' END, p.deleted, NOT p.active AND NOT p.deleted
    FROM person_profiles p
   WHERE p.person_id = ANY (p_ids[1:500])
     AND EXISTS (SELECT 1 FROM people_chats c WHERE c.person_id = taskiem_person_scope() AND c.peer_id = p.person_id);
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_people_names(uuid[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_people_names(uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_names(uuid[]) TO taskiem_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_match(p_viewer uuid, p_key bytea, p_hashes bytea[])
RETURNS TABLE (hash bytea, person_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_viewer IS NULL OR p_viewer IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'contacts are matched for a person in scope' USING ERRCODE = '42501';
  END IF;
  IF length(p_key) IS DISTINCT FROM 32 OR cardinality(p_hashes) > 1000 THEN
    RAISE EXCEPTION 'a 32-byte key and at most 1000 hashes' USING ERRCODE = '22023';
  END IF;
  RETURN QUERY
  SELECT h.h, p.user_id
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active' AND u.deleted_at IS NULL
    LEFT JOIN person_privacy s ON s.person_id = p.user_id
    JOIN (SELECT DISTINCT x AS h FROM unnest(p_hashes) x) h ON h.h = sha256(p_key || convert_to('taskiem-contacts:' || p.number, 'UTF8'))
   WHERE p.user_id <> p_viewer AND COALESCE(s.find_from_contacts, true)
     AND taskiem_people_allows(COALESCE(s.find_by_number, 'contacts'), p.user_id, p_viewer);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_business_photos(p_tenants uuid[])
RETURNS TABLE (tenant_id uuid, photo jsonb)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, taskiem_business_photo(t.id) FROM tenants t
   WHERE t.id = ANY (p_tenants[1:500]) AND taskiem_person_scope() IS NOT NULL AND taskiem_person_business() IS NULL
     AND EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = t.id AND c.person_id = taskiem_person_scope() AND c.line = 'app')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_customer_business_photos(uuid[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_customer_business_photos(uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_customer_business_photos(uuid[]) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_customer_business_photos(uuid[]);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_match(p_viewer uuid, p_key bytea, p_hashes bytea[])
RETURNS TABLE (hash bytea, person_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_viewer IS NULL OR p_viewer IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'contacts are matched for a person in scope' USING ERRCODE = '42501';
  END IF;
  IF length(p_key) IS DISTINCT FROM 32 OR cardinality(p_hashes) > 1000 THEN
    RAISE EXCEPTION 'a 32-byte key and at most 1000 hashes' USING ERRCODE = '22023';
  END IF;
  RETURN QUERY
  SELECT h.h, p.user_id
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active' AND u.deleted_at IS NULL
    LEFT JOIN person_privacy s ON s.person_id = p.user_id
    JOIN (SELECT DISTINCT x AS h FROM unnest(p_hashes) x) h ON h.h = sha256(p_key || convert_to('taskiem-contacts:' || p.number, 'UTF8'))
   WHERE p.user_id <> p_viewer AND COALESCE(s.find_from_contacts, true);
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_people_names(uuid[]);
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_names(p_ids uuid[])
RETURNS TABLE (person_id uuid, name text, gone boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF taskiem_person_scope() IS NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT p.person_id, CASE WHEN p.active AND NOT p.deleted THEN p.name ELSE '' END, p.deleted OR NOT p.active
    FROM person_profiles p
   WHERE p.person_id = ANY (p_ids[1:500])
     AND EXISTS (SELECT 1 FROM people_chats c WHERE c.person_id = taskiem_person_scope() AND c.peer_id = p.person_id);
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_people_names(uuid[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_people_names(uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_names(uuid[]) TO taskiem_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_reach(p_chat uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_peer uuid;
  v_conv uuid;
BEGIN
  IF taskiem_person_scope() IS NULL THEN
    RAISE EXCEPTION 'a people chat is reached from a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  IF taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'a business is in scope' USING ERRCODE = '42501';
  END IF;
  SELECT c.peer_id, c.conversation_id INTO v_peer, v_conv FROM people_chats c
   WHERE c.id = p_chat AND c.person_id = taskiem_person_scope();
  IF NOT FOUND OR v_peer IS NULL THEN
    RAISE EXCEPTION 'no such chat' USING ERRCODE = 'P0002';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = v_peer AND p.active AND NOT p.deleted) THEN
    RAISE EXCEPTION 'the account was deleted' USING ERRCODE = 'P0002', HINT = 'gone';
  END IF;
  PERFORM set_config('app.people_reach', v_conv::text, true);
  PERFORM set_config('app.people_peer', v_peer::text, true);
  RETURN v_peer;
END
$$;
-- +goose StatementEnd

UPDATE people_messages SET screen = 'warn' WHERE screen = 'transfer';
ALTER TABLE people_messages DROP CONSTRAINT people_messages_screen_check;
ALTER TABLE people_messages ADD CONSTRAINT people_messages_screen_check CHECK (screen IN ('warn'));

DROP INDEX people_chats_page;
ALTER TABLE people_chats DROP COLUMN empty;
ALTER TABLE people_chats DROP COLUMN peer_wrote_at;
