-- People chatting with people, one to one (docs/spec/people-chatting.md
-- §7, §8, §13, §14, stage 5a, stream A; decision 0069).
--
-- A people conversation between two persons has two copies, as a
-- customer's conversation with a business has (00452): each person's own
-- (people_chats, people_messages, people_files), under their own row-level
-- scope (taskiem_person_scope()), each message the same id on both. Each
-- copy's words, cards, places and files are sealed under its owner's own
-- data key (person_keys, the key itself sealed with the platform's root
-- key): kept by Taskiem and opened only by its servers, not end-to-end
-- encrypted.
--
-- The conversation's id is the pair's (taskiem_people_pair), so both
-- copies name the same conversation without either reading the other's.
-- The one crossing from one person's copy into the other's is
-- taskiem_people_reach: from a person's own transaction, for a
-- conversation of their own copies, it puts that one conversation of the
-- other person's copy (and their key, and their block of this person) in
-- reach for the rest of the transaction. A business, and a person's
-- transaction with a business's door open, reach no people chat.
--
-- Requests (§7, §8.2): a ledger of each sender's requests and what became
-- of them, kept past either copy and read only through definers, so a
-- sender's limits follow how often their requests are accepted, deleted or
-- blocked.
-- +goose Up

-- The conversation of two people: the same for both copies.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_pair(a uuid, b uuid) RETURNS uuid
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT md5('taskiem-people:' || LEAST(a, b)::text || ':' || GREATEST(a, b)::text)::uuid
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION taskiem_people_pair(uuid, uuid) TO taskiem_app, taskiem_dispatch;

-- The conversation, and the other person, in reach (set by
-- taskiem_people_reach only).
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_reached() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE AS $$
  SELECT NULLIF(current_setting('app.people_reach', true), '')::uuid
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_peer() RETURNS uuid
LANGUAGE sql STABLE PARALLEL SAFE AS $$
  SELECT NULLIF(current_setting('app.people_peer', true), '')::uuid
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION taskiem_people_reached() TO taskiem_app, taskiem_dispatch;
GRANT EXECUTE ON FUNCTION taskiem_people_peer() TO taskiem_app, taskiem_dispatch;

-- A person's own data key, sealed with the platform's root key for them
-- alone. Deleted with their account.
CREATE TABLE person_keys (
  person_id   uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  sealed      text NOT NULL,
  kms_key     text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE person_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON person_keys TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
-- Through the reach: the other person's copy of that one conversation
-- (SELECT, INSERT; never more).
CREATE POLICY peer_select ON person_keys FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer());
CREATE POLICY peer_insert ON person_keys FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer());
GRANT SELECT, INSERT, DELETE ON person_keys TO taskiem_app;

-- A person's copy of a people conversation.
CREATE TABLE people_chats (
  id               uuid PRIMARY KEY,
  person_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- The other person (null only if their users row was deleted by hand).
  peer_id          uuid REFERENCES users(id) ON DELETE SET NULL,
  conversation_id  uuid NOT NULL,
  -- This person started it.
  started_by_me    boolean NOT NULL,
  -- A request waiting for this person (a stranger's first message), until
  -- accepted (§7); and this person's own first message waiting as a
  -- request on the other's side.
  request_at       timestamptz,
  waiting_at       timestamptz,
  accepted_at      timestamptz,
  muted            boolean NOT NULL DEFAULT false,
  archived         boolean NOT NULL DEFAULT false,
  -- Deleted for me: hidden, with its messages gone, until a new one.
  hidden_at        timestamptz,
  last_message_at  timestamptz NOT NULL DEFAULT now(),
  created_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (person_id, conversation_id),
  CHECK (person_id IS DISTINCT FROM peer_id),
  CHECK (peer_id IS NULL OR conversation_id = taskiem_people_pair(person_id, peer_id))
);
CREATE UNIQUE INDEX people_chats_peer ON people_chats (person_id, peer_id);
CREATE INDEX people_chats_list ON people_chats (person_id, last_message_at DESC);
CREATE INDEX people_chats_started ON people_chats (person_id, created_at) WHERE started_by_me;
ALTER TABLE people_chats ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_chats FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON people_chats TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
-- Through the reach: the other person's copy of that one conversation
-- (SELECT, INSERT, UPDATE; never more).
CREATE POLICY peer_select ON people_chats FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached());
CREATE POLICY peer_insert ON people_chats FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached());
CREATE POLICY peer_update ON people_chats FOR UPDATE TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached())
  WITH CHECK (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached());
GRANT SELECT, INSERT, UPDATE, DELETE ON people_chats TO taskiem_app;
-- The reach reads only whose copy it is, with whom, of which conversation.
CREATE POLICY dispatch_read ON people_chats FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, person_id, peer_id, conversation_id) ON people_chats TO taskiem_dispatch;

-- A message of a person's copy. The same id on both copies.
CREATE TABLE people_messages (
  person_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id               uuid NOT NULL,
  chat_id          uuid NOT NULL REFERENCES people_chats(id) ON DELETE CASCADE,
  conversation_id  uuid NOT NULL,
  -- This person sent it.
  mine             boolean NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('text', 'media', 'card', 'location')),
  -- {body, card, location, name}: sealed under the owner's key; NULL once
  -- deleted for everyone.
  sealed           bytea,
  -- A photo, voice note or file: {kind, type, size} (its name is sealed).
  attachment       jsonb CHECK (attachment IS NULL OR jsonb_typeof(attachment) = 'object'),
  -- A card's type (product, business, order, delivery, split), for the
  -- list and for Mark as paid; its contents are sealed.
  card_type        text CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split')),
  CHECK ((kind = 'card') = (card_type IS NOT NULL)),
  reply_to         uuid,
  -- How many times it was forwarded on its way here (0: not forwarded).
  forwards         int NOT NULL DEFAULT 0 CHECK (forwards >= 0),
  client_id        text CHECK (length(client_id) <= 80),
  -- On this person's own message: when it reached the other's copy (two
  -- ticks; one tick while null), and when they read it (only once the
  -- conversation is accepted). On the other's message: when this person
  -- read it.
  delivered_at     timestamptz,
  read_at          timestamptz,
  starred_at       timestamptz,
  my_reaction      text CHECK (length(my_reaction) <= 16),
  their_reaction   text CHECK (length(their_reaction) <= 16),
  -- Deleted for everyone: nothing of it is kept but that it was.
  deleted_at       timestamptz,
  -- The stranger checks' verdict on a message to this person: warn.
  screen           text CHECK (screen IN ('warn')),
  -- A split card marked paid, and by whom.
  paid_at          timestamptz,
  paid_by_me       boolean,
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (person_id, id)
);
CREATE INDEX people_messages_stream ON people_messages (person_id, chat_id, created_at DESC);
CREATE INDEX people_messages_sent ON people_messages (person_id, chat_id, created_at) WHERE mine;
CREATE INDEX people_messages_unread ON people_messages (person_id, chat_id) WHERE NOT mine AND read_at IS NULL;
CREATE UNIQUE INDEX people_messages_client ON people_messages (person_id, chat_id, client_id) WHERE client_id IS NOT NULL;
ALTER TABLE people_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON people_messages TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
-- Through the reach: the other person's copy of that one conversation
-- (SELECT, INSERT, UPDATE; never more).
CREATE POLICY peer_select ON people_messages FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached());
CREATE POLICY peer_insert ON people_messages FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached());
CREATE POLICY peer_update ON people_messages FOR UPDATE TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached())
  WITH CHECK (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached());
GRANT SELECT, INSERT, UPDATE, DELETE ON people_messages TO taskiem_app;

-- A person's copy of a photo, voice note or file: its bytes sealed under
-- their key.
CREATE TABLE people_files (
  person_id        uuid NOT NULL,
  message_id       uuid NOT NULL,
  conversation_id  uuid NOT NULL,
  type             text NOT NULL CHECK (length(type) <= 100),
  size             bigint NOT NULL CHECK (size > 0 AND size <= 26214400),
  sealed           bytea NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (person_id, message_id),
  FOREIGN KEY (person_id, message_id) REFERENCES people_messages (person_id, id) ON DELETE CASCADE
);
ALTER TABLE people_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_files FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON people_files TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
-- Through the reach: the other person's copy of that one conversation
-- (SELECT, INSERT, DELETE; never more).
CREATE POLICY peer_select ON people_files FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached());
CREATE POLICY peer_insert ON people_files FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached());
CREATE POLICY peer_delete ON people_files FOR DELETE TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL AND person_id = taskiem_people_peer() AND conversation_id = taskiem_people_reached());
GRANT SELECT, INSERT, DELETE ON people_files TO taskiem_app;

-- People a person blocked (§8.1). The other person reads only whether
-- they themself are blocked, through the reach.
CREATE TABLE people_blocks (
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  blocked_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (person_id, blocked_id),
  CHECK (person_id <> blocked_id)
);
ALTER TABLE people_blocks ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_blocks FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON people_blocks TO taskiem_app
  USING (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
CREATE POLICY peer_reads ON people_blocks FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_person_scope() IS NOT NULL
    AND person_id = taskiem_people_peer() AND blocked_id = taskiem_person_scope());
GRANT SELECT, INSERT, DELETE ON people_blocks TO taskiem_app;

-- A person's own bank account for others to pay them back (split cards,
-- §11 A): as they typed it, sealed under their key, never checked by
-- Taskiem, which moves no money.
CREATE TABLE person_payout_accounts (
  person_id   uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  sealed      bytea NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE person_payout_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_payout_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON person_payout_accounts TO taskiem_app
  USING (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
GRANT SELECT, INSERT, UPDATE, DELETE ON person_payout_accounts TO taskiem_app;

-- Reports of a person or a people chat to Taskiem's moderators (§8.3): the
-- last 20 messages of the reporter's copy, opened, with their consent. The
-- reporter writes one and reads none back.
CREATE TABLE people_reports (
  id           uuid PRIMARY KEY,
  person_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  reported_id  uuid REFERENCES users(id) ON DELETE SET NULL,
  reason       text NOT NULL CHECK (length(reason) BETWEEN 1 AND 40),
  note         text NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
  consent      boolean NOT NULL CHECK (consent),
  messages     jsonb NOT NULL CHECK (jsonb_typeof(messages) = 'array'),
  -- Reported from a request.
  from_request boolean NOT NULL DEFAULT false,
  status       text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'reviewed')),
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX people_reports_open ON people_reports (created_at) WHERE status = 'open';
ALTER TABLE people_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_reports FORCE ROW LEVEL SECURITY;
CREATE POLICY person_writes ON people_reports FOR INSERT TO taskiem_app WITH CHECK (person_id = taskiem_person_scope());
GRANT INSERT ON people_reports TO taskiem_app;

-- Each sender's requests and what became of them (§8.2), kept past the
-- copies; only definers read or write it.
CREATE TABLE people_requests (
  sender_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  recipient_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  sent_at       timestamptz NOT NULL DEFAULT now(),
  outcome       text NOT NULL DEFAULT 'waiting' CHECK (outcome IN ('waiting', 'accepted', 'deleted', 'blocked')),
  decided_at    timestamptz,
  PRIMARY KEY (sender_id, recipient_id, sent_at)
);
-- One request waiting from a sender to a person at a time.
CREATE UNIQUE INDEX people_requests_waiting ON people_requests (sender_id, recipient_id) WHERE outcome = 'waiting';
CREATE INDEX people_requests_recipient ON people_requests (recipient_id);
ALTER TABLE people_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY no_app ON people_requests TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch_all ON people_requests TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON people_requests FROM PUBLIC;
GRANT SELECT (sender_id, recipient_id, sent_at, outcome, decided_at), INSERT, UPDATE, DELETE ON people_requests TO taskiem_dispatch;

-- What the definers may know of a person: their name, and whether their
-- account is active or was deleted. Kept from users by a trigger, so the
-- dispatch role, which may never read users' names, reads this alone.
CREATE TABLE person_profiles (
  person_id   uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  name        text NOT NULL DEFAULT '',
  active      boolean NOT NULL,
  deleted     boolean NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE person_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_profiles FORCE ROW LEVEL SECURITY;
CREATE POLICY no_app ON person_profiles TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch_all ON person_profiles TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON person_profiles FROM PUBLIC;
GRANT SELECT (person_id, name, active, deleted), INSERT, UPDATE ON person_profiles TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_profile_sync() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO person_profiles (person_id, name, active, deleted)
  VALUES (NEW.id, COALESCE(NEW.name, ''), NEW.status = 'active', NEW.deleted_at IS NOT NULL)
  ON CONFLICT (person_id) DO UPDATE SET name = EXCLUDED.name, active = EXCLUDED.active, deleted = EXCLUDED.deleted, updated_at = now();
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_person_profile_sync() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_person_profile_sync() FROM PUBLIC;
CREATE TRIGGER users_person_profile AFTER INSERT OR UPDATE OF name, status, deleted_at ON users
  FOR EACH ROW EXECUTE FUNCTION taskiem_person_profile_sync();
INSERT INTO person_profiles (person_id, name, active, deleted)
  SELECT id, COALESCE(name, ''), status = 'active', deleted_at IS NOT NULL FROM users
  ON CONFLICT (person_id) DO NOTHING;

-- Pushes on the person's side gain two kinds (§13): a person's message and
-- a request. The person's devices take them as they take 'you'.
ALTER TABLE push_outbox
  DROP CONSTRAINT push_outbox_side,
  DROP CONSTRAINT push_outbox_kind_check,
  ADD CONSTRAINT push_outbox_kind_check CHECK (kind IN ('business', 'customers', 'orders', 'approvals', 'alerts', 'you', 'person', 'request')),
  ADD CONSTRAINT push_outbox_side CHECK ((tenant_id IS NULL) = (kind IN ('you', 'person', 'request')));

-- Which pushes the person gets on You gains people's messages and requests.
ALTER TABLE person_notify ADD COLUMN people boolean NOT NULL DEFAULT true;
ALTER TABLE person_notify ADD COLUMN requests boolean NOT NULL DEFAULT true;

-- The reach (§13): from a person's own transaction, for one of their own
-- copies, the other person's copy of that conversation, their key and
-- their block of this person, for the rest of the transaction. Refused
-- with a business in scope or a door open, and for an account no longer
-- active (hint gone). Answers the other person.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_reach(p_chat uuid) RETURNS uuid
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

-- Whether a person can be written to: an active account, not deleted, not
-- the person asking. Nothing else of them.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_writable(p_person uuid) RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF taskiem_person_scope() IS NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  RETURN p_person IS DISTINCT FROM taskiem_person_scope()
     AND EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = p_person AND p.active AND NOT p.deleted);
END
$$;
-- +goose StatementEnd

-- The names of the people the person in scope has a copy with (a deleted
-- account's is empty, gone): nothing of anyone else.
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

-- The phone of someone the person in scope chats with, for showing it when
-- its owner chose to (the API asks finding people's policy first).
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_phone(p_peer uuid) RETURNS text
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v text;
BEGIN
  IF taskiem_person_scope() IS NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  SELECT p.number INTO v FROM user_phones p JOIN person_profiles f ON f.person_id = p.user_id AND f.active AND NOT f.deleted
   WHERE p.user_id = p_peer
     AND EXISTS (SELECT 1 FROM people_chats c WHERE c.person_id = taskiem_person_scope() AND c.peer_id = p_peer);
  RETURN v;
END
$$;
-- +goose StatementEnd

-- A request's ledger. 'waiting': the person in scope sent one to p_other
-- (a copy of theirs with them must exist). 'accepted', 'deleted',
-- 'blocked': the person in scope decided p_other's waiting request.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_request_mark(p_other uuid, p_outcome text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  me uuid := taskiem_person_scope();
BEGIN
  IF me IS NULL OR taskiem_person_business() IS NOT NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM people_chats c WHERE c.person_id = me AND c.peer_id = p_other) THEN
    RAISE EXCEPTION 'no such chat' USING ERRCODE = 'P0002';
  END IF;
  IF p_outcome = 'waiting' THEN
    -- A request already waiting stays the one; one decided before stays
    -- counted, and this is a new one.
    INSERT INTO people_requests (sender_id, recipient_id) VALUES (me, p_other)
    ON CONFLICT (sender_id, recipient_id) WHERE outcome = 'waiting' DO NOTHING;
  ELSIF p_outcome IN ('accepted', 'deleted', 'blocked') THEN
    UPDATE people_requests SET outcome = p_outcome, decided_at = now()
     WHERE sender_id = p_other AND recipient_id = me AND outcome = 'waiting';
  ELSE
    RAISE EXCEPTION 'no such outcome %', p_outcome USING ERRCODE = '22023';
  END IF;
END
$$;
-- +goose StatementEnd

-- The person in scope's requests as a sender: waiting now, sent in the
-- last day, accepted, and deleted or blocked.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_request_counts()
RETURNS TABLE (waiting int, today int, accepted int, refused int)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF taskiem_person_scope() IS NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT count(*) FILTER (WHERE r.outcome = 'waiting')::int,
         count(*) FILTER (WHERE r.sent_at > now() - interval '1 day')::int,
         count(*) FILTER (WHERE r.outcome = 'accepted')::int,
         count(*) FILTER (WHERE r.outcome IN ('deleted', 'blocked'))::int
    FROM people_requests r WHERE r.sender_id = taskiem_person_scope();
END
$$;
-- +goose StatementEnd

-- The card door (§10.1): a person opening a shared product or business
-- card reads it live as themself, through this door, narrower than the
-- customer door: the person in scope, no other door open, an active
-- business (not a workflows workspace) that shows itself to the public (a
-- published shop, or its Discover listing), and that has not blocked the
-- person. Audited in the business's chain; then that business is in scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_card_enter(p_person uuid, p_tenant uuid, p_what text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'the card door is for the person signed in' USING ERRCODE = '42501';
  END IF;
  IF taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'a door is already open in this transaction' USING ERRCODE = '42501';
  END IF;
  IF p_what NOT IN ('product', 'business') THEN
    RAISE EXCEPTION 'the card door has no card %', p_what USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = p_tenant AND status = 'active' AND kind = 'business')
     OR NOT EXISTS (SELECT 1 FROM users WHERE id = p_person AND status = 'active')
     OR NOT (EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_tenant AND s.status = 'published')
             OR taskiem_discover_open(p_tenant)) THEN
    RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
  END IF;
  IF EXISTS (SELECT 1 FROM customer_conversations c WHERE c.tenant_id = p_tenant AND c.person_id = p_person AND c.line = 'app' AND c.blocked_at IS NOT NULL) THEN
    RAISE EXCEPTION 'the business does not take messages from this customer' USING ERRCODE = '42501', HINT = 'blocked';
  END IF;
  PERFORM set_config('app.tenant_scope', '{' || p_tenant::text || '}', true);
  PERFORM taskiem_audit_append(p_tenant, 'person', p_person::text, 'customer.card', p_person::text,
    jsonb_build_object('card', p_what, 'door', 'card', 'from', 'people'));
  PERFORM set_config('app.person_business', p_tenant::text, true);
END
$$;
-- +goose StatementEnd

-- Deleting an account (00467) also ends the person's requests, sent and
-- received; their copies, key and blocks are deleted by the caller in
-- their own scope, in the same transaction.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_person_delete(p_person uuid, p_key bytea)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint := 0;
  m bigint;
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'an account is deleted by its own person' USING ERRCODE = '42501';
  END IF;
  IF current_setting('app.person_business', true) IS NOT NULL AND current_setting('app.person_business', true) <> '' THEN
    RAISE EXCEPTION 'a door is open in this transaction' USING ERRCODE = '42501';
  END IF;
  PERFORM 1 FROM users WHERE id = p_person AND status = 'active';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such account' USING ERRCODE = 'P0002';
  END IF;
  IF EXISTS (SELECT 1 FROM memberships m JOIN tenants t ON t.id = m.tenant_id AND t.status <> 'deleted'
              WHERE m.user_id = p_person AND m.role = 'owner') THEN
    RAISE EXCEPTION 'the person owns a business' USING ERRCODE = '55000', HINT = 'owns_business';
  END IF;
  IF EXISTS (SELECT 1 FROM memberships m JOIN tenants t ON t.id = m.tenant_id AND t.status <> 'deleted'
              WHERE m.user_id = p_person) THEN
    RAISE EXCEPTION 'the person has a role in a business' USING ERRCODE = '55000', HINT = 'in_business';
  END IF;
  IF length(p_key) = 32 THEN
    DELETE FROM customer_phone_index i USING user_phones p
     WHERE p.user_id = p_person AND i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'));
    GET DIAGNOSTICS m = ROW_COUNT;
    n := n + m;
  END IF;
  n := n + taskiem_person_orders_release(p_person);
  UPDATE storefront_orders o SET person_id = NULL WHERE o.person_id = p_person;
  UPDATE customer_conversations c SET person_id = NULL, person_left_at = now() WHERE c.person_id = p_person;
  UPDATE sessions s SET revoked_at = now() WHERE s.user_id = p_person AND s.revoked_at IS NULL;
  DELETE FROM user_phones WHERE user_id = p_person;
  DELETE FROM user_devices WHERE user_id = p_person;
  DELETE FROM second_proof_links WHERE user_id = p_person;
  DELETE FROM phone_codes WHERE user_id = p_person;
  DELETE FROM webauthn_credentials WHERE user_id = p_person;
  DELETE FROM whatsapp_pins WHERE user_id = p_person;
  DELETE FROM whatsapp_bindings WHERE user_id = p_person;
  DELETE FROM telegram_bindings WHERE user_id = p_person;
  DELETE FROM people_requests WHERE sender_id = p_person OR recipient_id = p_person;
  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_people_reach(uuid)',
                           'taskiem_people_writable(uuid)',
                           'taskiem_people_names(uuid[])',
                           'taskiem_people_phone(uuid)',
                           'taskiem_people_request_mark(uuid, text)',
                           'taskiem_people_request_counts()',
                           'taskiem_people_card_enter(uuid, uuid, text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- 00467's body.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_person_delete(p_person uuid, p_key bytea)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint := 0;
  m bigint;
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'an account is deleted by its own person' USING ERRCODE = '42501';
  END IF;
  IF current_setting('app.person_business', true) IS NOT NULL AND current_setting('app.person_business', true) <> '' THEN
    RAISE EXCEPTION 'a door is open in this transaction' USING ERRCODE = '42501';
  END IF;
  PERFORM 1 FROM users WHERE id = p_person AND status = 'active';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such account' USING ERRCODE = 'P0002';
  END IF;
  IF EXISTS (SELECT 1 FROM memberships m JOIN tenants t ON t.id = m.tenant_id AND t.status <> 'deleted'
              WHERE m.user_id = p_person AND m.role = 'owner') THEN
    RAISE EXCEPTION 'the person owns a business' USING ERRCODE = '55000', HINT = 'owns_business';
  END IF;
  IF EXISTS (SELECT 1 FROM memberships m JOIN tenants t ON t.id = m.tenant_id AND t.status <> 'deleted'
              WHERE m.user_id = p_person) THEN
    RAISE EXCEPTION 'the person has a role in a business' USING ERRCODE = '55000', HINT = 'in_business';
  END IF;
  IF length(p_key) = 32 THEN
    DELETE FROM customer_phone_index i USING user_phones p
     WHERE p.user_id = p_person AND i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'));
    GET DIAGNOSTICS m = ROW_COUNT;
    n := n + m;
  END IF;
  n := n + taskiem_person_orders_release(p_person);
  UPDATE storefront_orders o SET person_id = NULL WHERE o.person_id = p_person;
  UPDATE customer_conversations c SET person_id = NULL, person_left_at = now() WHERE c.person_id = p_person;
  UPDATE sessions s SET revoked_at = now() WHERE s.user_id = p_person AND s.revoked_at IS NULL;
  DELETE FROM user_phones WHERE user_id = p_person;
  DELETE FROM user_devices WHERE user_id = p_person;
  DELETE FROM second_proof_links WHERE user_id = p_person;
  DELETE FROM phone_codes WHERE user_id = p_person;
  DELETE FROM webauthn_credentials WHERE user_id = p_person;
  DELETE FROM whatsapp_pins WHERE user_id = p_person;
  DELETE FROM whatsapp_bindings WHERE user_id = p_person;
  DELETE FROM telegram_bindings WHERE user_id = p_person;
  RETURN n;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_people_card_enter(uuid, uuid, text);
DROP FUNCTION taskiem_people_request_counts();
DROP FUNCTION taskiem_people_request_mark(uuid, text);
DROP FUNCTION taskiem_people_phone(uuid);
DROP FUNCTION taskiem_people_names(uuid[]);
DROP FUNCTION taskiem_people_writable(uuid);
DROP FUNCTION taskiem_people_reach(uuid);
ALTER TABLE person_notify DROP COLUMN requests;
ALTER TABLE person_notify DROP COLUMN people;
DELETE FROM push_outbox WHERE kind IN ('person', 'request');
ALTER TABLE push_outbox
  DROP CONSTRAINT push_outbox_side,
  DROP CONSTRAINT push_outbox_kind_check,
  ADD CONSTRAINT push_outbox_kind_check CHECK (kind IN ('business', 'customers', 'orders', 'approvals', 'alerts', 'you')),
  ADD CONSTRAINT push_outbox_side CHECK ((tenant_id IS NULL) = (kind = 'you'));
DROP TRIGGER users_person_profile ON users;
DROP FUNCTION taskiem_person_profile_sync();
DROP TABLE person_profiles;
DROP TABLE people_requests;
DROP TABLE people_reports;
DROP TABLE person_payout_accounts;
DROP TABLE people_blocks;
DROP TABLE people_files;
DROP TABLE people_messages;
DROP TABLE people_chats;
DROP TABLE person_keys;
DROP FUNCTION taskiem_people_peer();
DROP FUNCTION taskiem_people_reached();
DROP FUNCTION taskiem_people_pair(uuid, uuid);
