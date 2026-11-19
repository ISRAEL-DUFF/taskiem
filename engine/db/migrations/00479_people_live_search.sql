-- People chatting with people, stage 5a's missing chat features
-- (docs/spec/people-chatting.md §6.2, §7, §13, "As built: stage 5a gaps,
-- chat features (server)"; decision 0069).
--
-- * You's live connection (GET /v1/me/live) reads what changed in the
--   person's own people chats since it last looked: changed_at on each
--   copy's chats and messages (set on every insert and update), and
--   people_gone for a message deleted for the person, or a request they
--   deleted (from another of their devices), kept an hour.
-- * person_live: the person's open live connections, each in front or
--   away, so presence follows them (online while one is in front).
-- * people_typing: who is typing to the person now, in which of their
--   chats, written into the person's copy through taskiem_people_reach
--   only within an accepted chat and never across a block (the API's
--   rules), gone after a few seconds.
-- * people_search: a per-person keyed index of the words of the person's
--   own copy: an HMAC of each normalised word's prefixes, under a key
--   derived from the person's own data key. Never the words. Written for
--   both copies of a message as it is kept (the other's through the
--   reach), removed with the message (delete for me, delete the chat,
--   the account) and when it is deleted for everyone.
--   people_search_ready: the person's messages from before the index were
--   indexed (on their first search).
-- * taskiem_customer_business_trust: what the businesses a person chats
--   with in the app have proved (the business chat head's trust line),
--   for You's one Chats list.
-- +goose Up

-- What changed, for the live connection.
ALTER TABLE people_chats ADD COLUMN changed_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE people_chats ALTER COLUMN changed_at SET DEFAULT clock_timestamp();
ALTER TABLE people_messages ADD COLUMN changed_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE people_messages ALTER COLUMN changed_at SET DEFAULT clock_timestamp();
CREATE INDEX people_chats_changed ON people_chats (person_id, changed_at);
CREATE INDEX people_messages_changed ON people_messages (person_id, changed_at);

-- +goose StatementBegin
CREATE FUNCTION taskiem_people_changed() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  NEW.changed_at := clock_timestamp();
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER people_chats_changed BEFORE UPDATE ON people_chats FOR EACH ROW EXECUTE FUNCTION taskiem_people_changed();
CREATE TRIGGER people_messages_changed BEFORE UPDATE ON people_messages FOR EACH ROW EXECUTE FUNCTION taskiem_people_changed();

-- A message deleted for the person (message_id), or a chat of theirs
-- gone (a request they deleted: message_id null), for their other
-- devices; kept an hour.
CREATE TABLE people_gone (
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  chat_id     uuid NOT NULL,
  message_id  uuid,
  at          timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX people_gone_at ON people_gone (person_id, at);

-- The person's open live connections: in front, or away.
CREATE TABLE person_live (
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id          uuid NOT NULL,
  front       boolean NOT NULL DEFAULT true,
  seen_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (person_id, id)
);

-- The search index's readiness: the person's older messages are indexed.
CREATE TABLE people_search_ready (
  person_id   uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  done_at     timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['people_gone', 'person_live', 'people_search_ready'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY person_own ON %I TO taskiem_app
      USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
      WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- Who is typing to the person (person_id), in their copy (chat_id), until
-- when. Written by the one typing through the reach.
CREATE TABLE people_typing (
  person_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  chat_id          uuid NOT NULL REFERENCES people_chats(id) ON DELETE CASCADE,
  conversation_id  uuid NOT NULL,
  until            timestamptz NOT NULL,
  PRIMARY KEY (person_id, chat_id)
);
CREATE INDEX people_typing_chat ON people_typing (chat_id);

-- The search index of the person's own copy: keyed tokens only.
CREATE TABLE people_search (
  person_id        uuid NOT NULL,
  message_id       uuid NOT NULL,
  conversation_id  uuid NOT NULL,
  token            bytea NOT NULL CHECK (length(token) = 16),
  PRIMARY KEY (person_id, token, message_id),
  FOREIGN KEY (person_id, message_id) REFERENCES people_messages (person_id, id) ON DELETE CASCADE
);
CREATE INDEX people_search_message ON people_search (person_id, message_id);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['people_typing', 'people_search'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY person_own ON %I TO taskiem_app
      USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
      WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())', t);
    -- Through the reach: the other person''s rows of that one conversation.
    EXECUTE format('CREATE POLICY peer_select ON %I FOR SELECT TO taskiem_app
      USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached())', t);
    EXECUTE format('CREATE POLICY peer_insert ON %I FOR INSERT TO taskiem_app
      WITH CHECK (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached())', t);
    EXECUTE format('CREATE POLICY peer_update ON %I FOR UPDATE TO taskiem_app
      USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached())
      WITH CHECK (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached())', t);
    EXECUTE format('CREATE POLICY peer_delete ON %I FOR DELETE TO taskiem_app
      USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached())', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- What the businesses the person chats with in the app have proved, as
-- the business chat head shows it: a phone, a check of the business, a
-- payment provider connected, and since when it is on Taskiem. Nothing
-- of any other business.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_business_trust(p_tenants uuid[])
RETURNS TABLE (tenant_id uuid, phone boolean, business boolean, payments boolean, since timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, taskiem_tenant_phone_verified(t.id),
         EXISTS (SELECT 1 FROM tenant_verifications v WHERE v.tenant_id = t.id AND v.level IN ('business', 'agent_onboarded')),
         EXISTS (SELECT 1 FROM connections c WHERE c.tenant_id = t.id AND c.environment = 'prod' AND c.status = 'active'
                   AND c.connector IN ('paystack', 'flutterwave', 'moniepoint', 'interswitch', 'opay', 'opaybusiness', 'remita')),
         t.created_at
    FROM tenants t
   WHERE t.id = ANY (p_tenants[1:500]) AND taskiem_person_scope() IS NOT NULL AND taskiem_person_business() IS NULL
     AND EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = t.id AND c.person_id = taskiem_person_scope() AND c.line = 'app')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_customer_business_trust(uuid[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_customer_business_trust(uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_customer_business_trust(uuid[]) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_customer_business_trust(uuid[]);
DROP TABLE people_search;
DROP TABLE people_typing;
DROP TABLE people_search_ready;
DROP TABLE person_live;
DROP TABLE people_gone;
DROP TRIGGER people_messages_changed ON people_messages;
DROP TRIGGER people_chats_changed ON people_chats;
DROP FUNCTION taskiem_people_changed();
DROP INDEX people_messages_changed;
DROP INDEX people_chats_changed;
ALTER TABLE people_messages DROP COLUMN changed_at;
ALTER TABLE people_chats DROP COLUMN changed_at;
