-- People chatting with people, stage 5b: groups on the server
-- (docs/spec/people-chatting.md §9, §13, §14, §17 Q7, Q16, Q17;
-- docs/spec/people-chatting-5b.md A and D, with the founder's answers A2
-- to A8; decision 0069 point 7). "As built: stage 5b, groups (server)".
--
-- A group keeps ONE copy of its messages (group_messages, group_files),
-- sealed under the group's own data key (group_keys, the key itself sealed
-- with the platform's root key for that group alone), with each member's
-- own marks beside it: their read and delivered marks and settings on
-- their membership row (group_members: read_upto, delivered_upto, muted,
-- archived, mentions, hidden_at), a star or "deleted for me" per message
-- (group_marks, theirs alone), and their reaction (group_reactions, seen
-- by the members). Kept by Taskiem, opened only by its servers: not end
-- to end encrypted.
--
-- Who sees what is row-level security, not the API alone:
-- * a member (or someone who was one) sees the group's messages only
--   within their membership's spans (group_members.visible, a multirange
--   of the times they were in it): from when they joined (§17 Q16), and,
--   after leaving or being removed, up to then (A5), until they delete
--   their copy. "Delete the chat for me" takes the time up to then out of
--   their spans;
-- * only a current member writes (a message, a reaction, typing), and
--   only an admin changes others' membership (taskiem_group_member_guard);
-- * someone invited sees the group's name and photo (not its messages)
--   while the invite waits, unless the invite is one they are never shown
--   (a block: A8);
-- * a group link is found only by its keyed hash, through a definer.
--
-- Group ids are uuids apart from any business's: a group report (next
-- slice) names group_id and, when it is about a member, person_id.
-- +goose Up

-- --- the person's side ---

-- Who can add me to groups (A3): "My contacts" by default (only people I
-- added), everyone, or nobody; anyone else's add becomes an invite that
-- waits in my Requests.
ALTER TABLE person_privacy ADD COLUMN groups_add text NOT NULL DEFAULT 'contacts' CHECK (groups_add IN ('everyone', 'contacts', 'nobody'));
GRANT SELECT (groups_add) ON person_privacy TO taskiem_dispatch;

-- Pushes of group messages (an invite is a request: person_notify.requests).
ALTER TABLE person_notify ADD COLUMN groups boolean NOT NULL DEFAULT true;

-- Two more kinds of push on the person's side (§13): a group's message,
-- and an invite to a group waiting in Requests.
ALTER TABLE push_outbox
  DROP CONSTRAINT push_outbox_side,
  DROP CONSTRAINT push_outbox_kind_check,
  ADD CONSTRAINT push_outbox_kind_check CHECK (kind IN ('business', 'customers', 'orders', 'approvals', 'alerts', 'you', 'person', 'request', 'group', 'group_invite')),
  ADD CONSTRAINT push_outbox_side CHECK ((tenant_id IS NULL) = (kind IN ('you', 'person', 'request', 'group', 'group_invite')));

-- --- the group ---

CREATE TABLE people_groups (
  id                uuid PRIMARY KEY,
  -- {name, description}, sealed under the group's key.
  sealed            bytea NOT NULL,
  created_by        uuid REFERENCES users(id) ON DELETE SET NULL,
  -- Who may post (§9): everyone, or admins only.
  admins_only_post  boolean NOT NULL DEFAULT false,
  -- Who may change the name, photo and description (A2): admins only by
  -- default.
  admins_only_edit  boolean NOT NULL DEFAULT true,
  -- Joining by the link waits for an admin (A4); at once by default.
  approve_joins     boolean NOT NULL DEFAULT false,
  photo_at          timestamptz,
  last_message_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
  created_at        timestamptz NOT NULL DEFAULT now(),
  changed_at        timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- The group's data key, sealed with the platform's root key for this group
-- alone (purpose group-key:{id}). Deleted with the group.
CREATE TABLE group_keys (
  group_id    uuid PRIMARY KEY REFERENCES people_groups(id) ON DELETE CASCADE,
  sealed      text NOT NULL,
  kms_key     text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- A member, or someone who was one (left or removed: their copy up to then
-- stays theirs until they delete it, A5). A deleted account's row goes
-- (§14): its messages stay, shown as "Deleted account".
CREATE TABLE group_members (
  group_id        uuid NOT NULL REFERENCES people_groups(id) ON DELETE CASCADE,
  person_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role            text NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
  -- How they came in, and who brought them: created, added (by an admin),
  -- invite (accepted), link (joined by the group's link, or approved).
  how             text NOT NULL CHECK (how IN ('created', 'added', 'invite', 'link')),
  added_by        uuid,
  -- The start of their current (or last) time in the group.
  joined_at       timestamptz NOT NULL DEFAULT clock_timestamp(),
  -- Left or removed, when, and by whom (removed).
  left_at         timestamptz,
  left_how        text CHECK (left_how IN ('left', 'removed')),
  removed_by      uuid,
  -- The times whose messages they see: each time they were in the group,
  -- less what they deleted for themselves.
  visible         tstzmultirange NOT NULL,
  -- Their own settings and marks.
  muted           boolean NOT NULL DEFAULT false,
  archived        boolean NOT NULL DEFAULT false,
  -- A mention pushes even while muted (A7), unless turned off here.
  mentions        boolean NOT NULL DEFAULT true,
  -- The chat deleted for them: out of their list until a new message.
  hidden_at       timestamptz,
  -- Read and delivered up to (the time of the latest message they had).
  read_upto       timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00',
  delivered_upto  timestamptz NOT NULL DEFAULT '1970-01-01 00:00:00+00',
  changed_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (group_id, person_id),
  CHECK ((left_at IS NULL) = (left_how IS NULL))
);
CREATE INDEX group_members_person ON group_members (person_id, group_id);
CREATE INDEX group_members_changed ON group_members (person_id, changed_at);
CREATE INDEX group_members_current ON group_members (group_id, joined_at) WHERE left_at IS NULL;

-- The group's one copy of its messages. sender_id stays when the sender's
-- account is deleted (the tombstone): shown "Deleted account".
CREATE TABLE group_messages (
  group_id    uuid NOT NULL REFERENCES people_groups(id) ON DELETE CASCADE,
  id          uuid NOT NULL,
  sender_id   uuid REFERENCES users(id) ON DELETE SET NULL,
  -- event: someone joined, left, was added or removed, made admin, or the
  -- group's info or settings changed ({type, actor, target} in event).
  kind        text NOT NULL CHECK (kind IN ('text', 'media', 'card', 'location', 'event')),
  -- {body, card, location, name}: sealed under the group's key; NULL once
  -- deleted for everyone, and for an event.
  sealed      bytea,
  event       jsonb CHECK (event IS NULL OR jsonb_typeof(event) = 'object'),
  CHECK ((kind = 'event') = (event IS NOT NULL)),
  attachment  jsonb CHECK (attachment IS NULL OR jsonb_typeof(attachment) = 'object'),
  card_type   text CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split')),
  CHECK ((kind = 'card') = (card_type IS NOT NULL)),
  reply_to    uuid,
  forwards    int NOT NULL DEFAULT 0 CHECK (forwards >= 0),
  client_id   text CHECK (length(client_id) <= 80),
  -- The members it mentions (@name), by id (A7).
  mentions    uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(mentions) <= 256),
  deleted_at  timestamptz,
  -- A share card marked paid, and by whom.
  paid_at     timestamptz,
  paid_by     uuid,
  -- clock_timestamp(), taken after the group's row is locked: messages are
  -- in the order they were kept, so read and delivered marks (a time) are
  -- exact.
  created_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
  changed_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (group_id, id)
);
CREATE INDEX group_messages_stream ON group_messages (group_id, created_at DESC, id DESC);
CREATE INDEX group_messages_changed ON group_messages (group_id, changed_at);
CREATE INDEX group_messages_sender ON group_messages (group_id, sender_id, created_at);
CREATE UNIQUE INDEX group_messages_client ON group_messages (group_id, sender_id, client_id) WHERE client_id IS NOT NULL;

-- A photo, voice note or file of a group message: its bytes sealed under
-- the group's key.
CREATE TABLE group_files (
  group_id    uuid NOT NULL,
  message_id  uuid NOT NULL,
  type        text NOT NULL CHECK (length(type) <= 100),
  size        bigint NOT NULL CHECK (size > 0 AND size <= 26214400),
  sealed      bytea NOT NULL,
  PRIMARY KEY (group_id, message_id),
  FOREIGN KEY (group_id, message_id) REFERENCES group_messages (group_id, id) ON DELETE CASCADE
);

-- A member's own marks on one message: starred, deleted for them.
CREATE TABLE group_marks (
  group_id    uuid NOT NULL,
  message_id  uuid NOT NULL,
  person_id   uuid NOT NULL,
  starred_at  timestamptz,
  hidden_at   timestamptz,
  changed_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (group_id, message_id, person_id),
  FOREIGN KEY (group_id, message_id) REFERENCES group_messages (group_id, id) ON DELETE CASCADE,
  FOREIGN KEY (group_id, person_id) REFERENCES group_members (group_id, person_id) ON DELETE CASCADE
);
CREATE INDEX group_marks_starred ON group_marks (person_id, starred_at) WHERE starred_at IS NOT NULL;
CREATE INDEX group_marks_changed ON group_marks (person_id, changed_at);

-- A member's reaction to a message, seen by the members who see it.
CREATE TABLE group_reactions (
  group_id    uuid NOT NULL,
  message_id  uuid NOT NULL,
  person_id   uuid NOT NULL,
  reaction    text NOT NULL CHECK (length(reaction) BETWEEN 1 AND 16),
  at          timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (group_id, message_id, person_id),
  FOREIGN KEY (group_id, message_id) REFERENCES group_messages (group_id, id) ON DELETE CASCADE,
  FOREIGN KEY (group_id, person_id) REFERENCES group_members (group_id, person_id) ON DELETE CASCADE
);

-- When each member's read (or delivered) mark moved, for "Read by" (A6):
-- written only for a member whose read ticks are on; a member's own rows
-- older than 31 days go as new ones come.
CREATE TABLE group_reads (
  group_id   uuid NOT NULL,
  person_id  uuid NOT NULL,
  kind       text NOT NULL CHECK (kind IN ('delivered', 'read')),
  upto       timestamptz NOT NULL,
  at         timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (group_id, person_id, kind, upto),
  FOREIGN KEY (group_id, person_id) REFERENCES group_members (group_id, person_id) ON DELETE CASCADE
);

-- An invite to a group, waiting in the person's Requests (A3). hidden: an
-- add across a block (A8): kept so the admin sees it as any invite, never
-- shown to the person.
CREATE TABLE group_invites (
  group_id    uuid NOT NULL REFERENCES people_groups(id) ON DELETE CASCADE,
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  invited_by  uuid,
  hidden      boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
  changed_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (group_id, person_id)
);
CREATE INDEX group_invites_person ON group_invites (person_id, changed_at);

-- Someone who opened the group's link while "Approve new members" is on
-- (A4), waiting for an admin.
CREATE TABLE group_join_requests (
  group_id    uuid NOT NULL REFERENCES people_groups(id) ON DELETE CASCADE,
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (group_id, person_id)
);

-- The group's invite link, kept as a person's link is: the code's keyed
-- hash and the nonce it is spelt from; one live link; a replaced one says
-- it no longer works (410) for a while; turned off, none is live.
CREATE TABLE group_links (
  id           uuid PRIMARY KEY,
  group_id     uuid NOT NULL REFERENCES people_groups(id) ON DELETE CASCADE,
  code_hash    bytea NOT NULL UNIQUE CHECK (length(code_hash) = 32),
  nonce        bytea NOT NULL CHECK (length(nonce) = 16),
  created_by   uuid,
  created_at   timestamptz NOT NULL DEFAULT now(),
  replaced_at  timestamptz
);
CREATE UNIQUE INDEX group_links_live ON group_links (group_id) WHERE replaced_at IS NULL;

-- The group's photo, sealed under the group's key.
CREATE TABLE group_photos (
  group_id    uuid PRIMARY KEY REFERENCES people_groups(id) ON DELETE CASCADE,
  type        text NOT NULL CHECK (type IN ('image/jpeg', 'image/png')),
  sealed      bytea NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- Who is typing in the group now.
CREATE TABLE group_typing (
  group_id   uuid NOT NULL,
  person_id  uuid NOT NULL,
  until      timestamptz NOT NULL,
  PRIMARY KEY (group_id, person_id),
  FOREIGN KEY (group_id, person_id) REFERENCES group_members (group_id, person_id) ON DELETE CASCADE
);

-- The search index of the group's copy: keyed tokens only (never words),
-- under a key derived from the group's own key; read by a member only for
-- the messages they see (the policy goes through group_messages').
CREATE TABLE group_search (
  group_id    uuid NOT NULL,
  message_id  uuid NOT NULL,
  token       bytea NOT NULL CHECK (length(token) = 16),
  PRIMARY KEY (group_id, token, message_id),
  FOREIGN KEY (group_id, message_id) REFERENCES group_messages (group_id, id) ON DELETE CASCADE
);
CREATE INDEX group_search_message ON group_search (group_id, message_id);

-- What changed, for the live connection.
CREATE TRIGGER people_groups_changed BEFORE UPDATE ON people_groups FOR EACH ROW EXECUTE FUNCTION taskiem_people_changed();
CREATE TRIGGER group_members_changed BEFORE UPDATE ON group_members FOR EACH ROW EXECUTE FUNCTION taskiem_people_changed();
CREATE TRIGGER group_messages_changed BEFORE UPDATE ON group_messages FOR EACH ROW EXECUTE FUNCTION taskiem_people_changed();
CREATE TRIGGER group_marks_changed BEFORE UPDATE ON group_marks FOR EACH ROW EXECUTE FUNCTION taskiem_people_changed();
CREATE TRIGGER group_invites_changed BEFORE UPDATE ON group_invites FOR EACH ROW EXECUTE FUNCTION taskiem_people_changed();

-- --- who is in the group (definers the policies use) ---

-- What the definers read of a membership, and what they write.
CREATE POLICY dispatch_all ON group_members TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (group_id, person_id, role, how, left_at, left_how, joined_at, visible), INSERT, UPDATE ON group_members TO taskiem_dispatch;
CREATE POLICY dispatch_all ON people_groups TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (id, approve_joins, photo_at, sealed), INSERT, UPDATE (last_message_at) ON people_groups TO taskiem_dispatch;
CREATE POLICY dispatch_all ON group_keys TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (group_id, sealed, kms_key), INSERT ON group_keys TO taskiem_dispatch;
CREATE POLICY dispatch_all ON group_invites TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (group_id, person_id, hidden, invited_by), DELETE ON group_invites TO taskiem_dispatch;
CREATE POLICY dispatch_all ON group_join_requests TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (group_id, person_id), INSERT ON group_join_requests TO taskiem_dispatch;
CREATE POLICY dispatch_read ON group_links FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (group_id, code_hash, replaced_at) ON group_links TO taskiem_dispatch;
CREATE POLICY dispatch_read ON group_photos FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (group_id, type, sealed) ON group_photos TO taskiem_dispatch;

-- Whether the person in scope is, or was, in the group (a row of theirs).
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_member(p_group uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT taskiem_person_scope() IS NOT NULL AND taskiem_person_business() IS NULL
     AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = p_group AND m.person_id = taskiem_person_scope())
$$;
-- +goose StatementEnd

-- Whether the person in scope is an admin of the group now.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_admin(p_group uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT taskiem_person_scope() IS NOT NULL AND taskiem_person_business() IS NULL
     AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = p_group AND m.person_id = taskiem_person_scope()
                   AND m.left_at IS NULL AND m.role = 'admin')
$$;
-- +goose StatementEnd

-- Whether the person in scope has an invite to the group they may see.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_invited(p_group uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT taskiem_person_scope() IS NOT NULL AND taskiem_person_business() IS NULL
     AND EXISTS (SELECT 1 FROM group_invites i WHERE i.group_id = p_group AND i.person_id = taskiem_person_scope() AND NOT i.hidden)
$$;
-- +goose StatementEnd

-- A person (not an admin) changes only their own settings and marks, and
-- may leave; never their role, when they joined, what they see, or come
-- back once they left (that is an admin's add, an invite or the link,
-- through the definers below, which run as taskiem_dispatch).
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_member_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF current_user <> 'taskiem_app' THEN
    RETURN NEW;
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NOT taskiem_group_admin(NEW.group_id) THEN
      RAISE EXCEPTION 'only an admin adds people to a group' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
  END IF;
  IF taskiem_group_admin(NEW.group_id) THEN
    RETURN NEW;
  END IF;
  IF NEW.person_id <> OLD.person_id OR NEW.group_id <> OLD.group_id OR NEW.role <> OLD.role OR NEW.how <> OLD.how
     OR NEW.added_by IS DISTINCT FROM OLD.added_by OR NEW.joined_at <> OLD.joined_at
     OR (OLD.left_at IS NOT NULL AND NEW.left_at IS DISTINCT FROM OLD.left_at)
     OR (OLD.left_at IS NULL AND NEW.left_at IS NOT NULL AND NEW.left_how <> 'left')
     OR NEW.removed_by IS DISTINCT FROM OLD.removed_by
     OR NOT (NEW.visible <@ OLD.visible) THEN
    RAISE EXCEPTION 'a member changes only their own settings' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER group_members_guard BEFORE INSERT OR UPDATE ON group_members FOR EACH ROW EXECUTE FUNCTION taskiem_group_member_guard();

-- --- the policies ---

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['people_groups', 'group_keys', 'group_members', 'group_messages', 'group_files', 'group_marks',
                           'group_reactions', 'group_reads', 'group_invites', 'group_join_requests', 'group_links',
                           'group_photos', 'group_typing', 'group_search'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- The group: seen by its members (and those who were), and by someone with
-- an invite they may see; changed by a current member (the API decides
-- who may change what); made only through taskiem_group_create; deleted by
-- its last member leaving.
CREATE POLICY member_read ON people_groups FOR SELECT TO taskiem_app
  USING (taskiem_group_member(id) OR taskiem_group_invited(id));
CREATE POLICY member_update ON people_groups FOR UPDATE TO taskiem_app
  USING (EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = people_groups.id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL))
  WITH CHECK (EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = people_groups.id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL));
CREATE POLICY member_delete ON people_groups FOR DELETE TO taskiem_app
  USING (EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = people_groups.id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL));
GRANT SELECT, UPDATE, DELETE ON people_groups TO taskiem_app;

CREATE POLICY member_read ON group_keys FOR SELECT TO taskiem_app
  USING (taskiem_group_member(group_id) OR taskiem_group_invited(group_id));
GRANT SELECT ON group_keys TO taskiem_app;

-- Memberships: one's own row, and the rows of a group one is or was in.
-- Added (insert) and changed for others only by an admin; one's own
-- settings by oneself (taskiem_group_member_guard); deleted only by
-- oneself (deleting one's copy, or one's account).
CREATE POLICY member_read ON group_members FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND (person_id = taskiem_person_scope() OR taskiem_group_member(group_id)));
CREATE POLICY admin_insert ON group_members FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_group_admin(group_id));
CREATE POLICY own_update ON group_members FOR UPDATE TO taskiem_app
  USING (taskiem_person_business() IS NULL AND (person_id = taskiem_person_scope() OR taskiem_group_admin(group_id)))
  WITH CHECK (taskiem_person_business() IS NULL AND (person_id = taskiem_person_scope() OR taskiem_group_admin(group_id)));
CREATE POLICY own_delete ON group_members FOR DELETE TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
GRANT SELECT, INSERT, UPDATE, DELETE ON group_members TO taskiem_app;

-- Messages: seen only within the person's own spans in the group; written
-- by a current member as themself; changed (deleted for everyone, a share
-- marked paid) by a current member.
CREATE POLICY member_read ON group_messages FOR SELECT TO taskiem_app
  USING (EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_messages.group_id AND m.person_id = taskiem_person_scope()
                   AND taskiem_person_business() IS NULL AND m.visible @> group_messages.created_at));
CREATE POLICY member_insert ON group_messages FOR INSERT TO taskiem_app
  WITH CHECK (sender_id = taskiem_person_scope() AND taskiem_person_business() IS NULL
    AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_messages.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL));
CREATE POLICY member_update ON group_messages FOR UPDATE TO taskiem_app
  USING (taskiem_person_business() IS NULL
    AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_messages.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL
                  AND m.visible @> group_messages.created_at))
  WITH CHECK (taskiem_person_business() IS NULL
    AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_messages.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL));
GRANT SELECT, INSERT, UPDATE ON group_messages TO taskiem_app;

-- A message's file, its search tokens and its reactions follow the message:
-- seen where it is seen (the subquery goes through group_messages' own
-- policy); written by a current member.
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['group_files', 'group_search'] LOOP
    EXECUTE format('CREATE POLICY member_read ON %I FOR SELECT TO taskiem_app
      USING (EXISTS (SELECT 1 FROM group_messages g WHERE g.group_id = %I.group_id AND g.id = %I.message_id))', t, t, t);
    EXECUTE format('CREATE POLICY member_insert ON %I FOR INSERT TO taskiem_app
      WITH CHECK (taskiem_person_business() IS NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = %I.group_id
        AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL))', t, t);
    EXECUTE format('CREATE POLICY member_delete ON %I FOR DELETE TO taskiem_app
      USING (taskiem_person_business() IS NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = %I.group_id
        AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL))', t, t);
    EXECUTE format('GRANT SELECT, INSERT, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

CREATE POLICY member_read ON group_reactions FOR SELECT TO taskiem_app
  USING (EXISTS (SELECT 1 FROM group_messages g WHERE g.group_id = group_reactions.group_id AND g.id = group_reactions.message_id));
CREATE POLICY own_write ON group_reactions FOR ALL TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope()
    AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_reactions.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL));
GRANT SELECT, INSERT, UPDATE, DELETE ON group_reactions TO taskiem_app;

-- A member's own marks, and typing; their read and delivered times are
-- seen by the group's members (the API shows them only to a message's
-- sender, as read ticks allow).
CREATE POLICY own_all ON group_marks TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
GRANT SELECT, INSERT, UPDATE, DELETE ON group_marks TO taskiem_app;
CREATE POLICY member_read ON group_reads FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_group_member(group_id));
CREATE POLICY own_write ON group_reads FOR ALL TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
GRANT SELECT, INSERT, DELETE ON group_reads TO taskiem_app;
CREATE POLICY member_read ON group_typing FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_group_member(group_id));
CREATE POLICY own_write ON group_typing FOR ALL TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope()
    AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_typing.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL));
GRANT SELECT, INSERT, UPDATE, DELETE ON group_typing TO taskiem_app;

-- Invites: the person's own (not one they are never shown), and every one
-- of a group to its admins; made and taken back by an admin, answered by
-- the person.
CREATE POLICY own_or_admin_read ON group_invites FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND ((person_id = taskiem_person_scope() AND NOT hidden) OR taskiem_group_admin(group_id)));
CREATE POLICY admin_insert ON group_invites FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_group_admin(group_id));
CREATE POLICY admin_update ON group_invites FOR UPDATE TO taskiem_app
  USING (taskiem_group_admin(group_id)) WITH CHECK (taskiem_group_admin(group_id));
CREATE POLICY own_or_admin_delete ON group_invites FOR DELETE TO taskiem_app
  USING (taskiem_person_business() IS NULL AND ((person_id = taskiem_person_scope() AND NOT hidden) OR taskiem_group_admin(group_id)));
GRANT SELECT, INSERT, UPDATE, DELETE ON group_invites TO taskiem_app;

CREATE POLICY own_or_admin ON group_join_requests FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND (person_id = taskiem_person_scope() OR taskiem_group_admin(group_id)));
CREATE POLICY own_or_admin_delete ON group_join_requests FOR DELETE TO taskiem_app
  USING (taskiem_person_business() IS NULL AND (person_id = taskiem_person_scope() OR taskiem_group_admin(group_id)));
GRANT SELECT, DELETE ON group_join_requests TO taskiem_app;

-- The link: an admin's.
CREATE POLICY admin_all ON group_links TO taskiem_app
  USING (taskiem_group_admin(group_id)) WITH CHECK (taskiem_group_admin(group_id));
GRANT SELECT, INSERT, UPDATE, DELETE ON group_links TO taskiem_app;

-- The photo: seen as the group is; changed by a current member (the API
-- decides who may edit).
CREATE POLICY member_read ON group_photos FOR SELECT TO taskiem_app
  USING (taskiem_group_member(group_id) OR taskiem_group_invited(group_id));
CREATE POLICY member_write ON group_photos FOR ALL TO taskiem_app
  USING (taskiem_person_business() IS NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_photos.group_id
    AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL))
  WITH CHECK (taskiem_person_business() IS NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_photos.group_id
    AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL));
GRANT SELECT, INSERT, UPDATE, DELETE ON group_photos TO taskiem_app;

-- --- definers ---

-- Makes a group: its sealed info and key (made and sealed by the server),
-- with the person in scope as its first member and admin.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_create(p_group uuid, p_sealed bytea, p_key_sealed text, p_kms_key text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_person_scope();
BEGIN
  IF v_me IS NULL OR taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'a group is made by a person in their own transaction' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = v_me AND p.active AND NOT p.deleted) THEN
    RAISE EXCEPTION 'the account is not active' USING ERRCODE = '42501';
  END IF;
  INSERT INTO people_groups (id, sealed, created_by) VALUES (p_group, p_sealed, v_me);
  INSERT INTO group_keys (group_id, sealed, kms_key) VALUES (p_group, p_key_sealed, p_kms_key);
  INSERT INTO group_members (group_id, person_id, role, how, visible)
  VALUES (p_group, v_me, 'admin', 'created', tstzmultirange(tstzrange(clock_timestamp(), NULL)));
END
$$;
-- +goose StatementEnd

-- A group's link, by its keyed code hash, for anyone holding the link: the
-- group, when the link was replaced (or turned off), what the preview
-- shows (the sealed name, the photo's time, the member count) and the
-- group's sealed key to open it with. Nothing of the members.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_link_find(p_hash bytea)
RETURNS TABLE (group_id uuid, replaced_at timestamptz, sealed bytea, key_sealed text, kms_key text, members int, photo_at timestamptz, approve_joins boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.group_id, l.replaced_at, g.sealed, k.sealed, k.kms_key,
         (SELECT count(*)::int FROM group_members m WHERE m.group_id = g.id AND m.left_at IS NULL),
         g.photo_at, g.approve_joins
    FROM group_links l JOIN people_groups g ON g.id = l.group_id JOIN group_keys k ON k.group_id = g.id
   WHERE l.code_hash = p_hash AND length(p_hash) = 32
$$;
-- +goose StatementEnd

-- A group's photo by its link's hash (a live link only), sealed, with the
-- group's sealed key.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_link_photo(p_hash bytea)
RETURNS TABLE (group_id uuid, type text, sealed bytea, key_sealed text, kms_key text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.group_id, p.type, p.sealed, k.sealed, k.kms_key
    FROM group_links l JOIN group_photos p ON p.group_id = l.group_id JOIN group_keys k ON k.group_id = l.group_id
   WHERE l.code_hash = p_hash AND length(p_hash) = 32 AND l.replaced_at IS NULL
$$;
-- +goose StatementEnd

-- Joining a group by its live link, as the person in scope (A4): joined at
-- once; or waiting for an admin when the group approves new members, or
-- when an admin removed them before or blocked them (state waiting, the
-- same either way); member when they are in it; full at p_max members;
-- busy past p_per_hour link joins in the last hour; gone for a replaced
-- or turned off link; none for no such link.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_join(p_hash bytea, p_max int, p_per_hour int)
RETURNS TABLE (group_id uuid, state text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_person_scope();
  v_group uuid;
  v_replaced timestamptz;
  v_approve boolean;
  v_left timestamptz;
  v_how text;
  v_found boolean;
BEGIN
  IF v_me IS NULL OR taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'a group is joined by a person in their own transaction' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = v_me AND p.active AND NOT p.deleted) THEN
    RAISE EXCEPTION 'the account is not active' USING ERRCODE = '42501';
  END IF;
  SELECT l.group_id, l.replaced_at INTO v_group, v_replaced FROM group_links l WHERE l.code_hash = p_hash;
  IF NOT FOUND THEN
    RETURN QUERY SELECT NULL::uuid, 'none'::text;
    RETURN;
  END IF;
  IF v_replaced IS NOT NULL THEN
    RETURN QUERY SELECT v_group, 'gone'::text;
    RETURN;
  END IF;
  -- The group's row is every writer's first lock (the API's order).
  SELECT g.approve_joins INTO v_approve FROM people_groups g WHERE g.id = v_group FOR UPDATE;
  SELECT m.left_at, m.left_how INTO v_left, v_how FROM group_members m WHERE m.group_id = v_group AND m.person_id = v_me;
  v_found := FOUND;
  IF v_found AND v_left IS NULL THEN
    RETURN QUERY SELECT v_group, 'member'::text;
    RETURN;
  END IF;
  IF (SELECT count(*) FROM group_members m WHERE m.group_id = v_group AND m.left_at IS NULL) >= p_max THEN
    RETURN QUERY SELECT v_group, 'full'::text;
    RETURN;
  END IF;
  IF v_approve OR (v_found AND v_how = 'removed')
     OR EXISTS (SELECT 1 FROM group_members m JOIN people_blocks b ON b.person_id = m.person_id AND b.blocked_id = v_me
                 WHERE m.group_id = v_group AND m.left_at IS NULL AND m.role = 'admin') THEN
    INSERT INTO group_join_requests (group_id, person_id) VALUES (v_group, v_me) ON CONFLICT DO NOTHING;
    RETURN QUERY SELECT v_group, 'waiting'::text;
    RETURN;
  END IF;
  IF (SELECT count(*) FROM group_members m WHERE m.group_id = v_group AND m.how = 'link' AND m.joined_at > now() - interval '1 hour') >= p_per_hour THEN
    RETURN QUERY SELECT v_group, 'busy'::text;
    RETURN;
  END IF;
  IF v_found THEN
    UPDATE group_members m SET left_at = NULL, left_how = NULL, removed_by = NULL, how = 'link', added_by = NULL, role = 'member',
           joined_at = clock_timestamp(), hidden_at = NULL, visible = m.visible + tstzmultirange(tstzrange(clock_timestamp(), NULL))
     WHERE m.group_id = v_group AND m.person_id = v_me;
  ELSE
    INSERT INTO group_members (group_id, person_id, how, visible) VALUES (v_group, v_me, 'link', tstzmultirange(tstzrange(clock_timestamp(), NULL)));
  END IF;
  DELETE FROM group_invites i WHERE i.group_id = v_group AND i.person_id = v_me;
  RETURN QUERY SELECT v_group, 'joined'::text;
END
$$;
-- +goose StatementEnd

-- Accepting an invite the person in scope may see (A3): they join the
-- group from now. full at p_max members; none without such an invite.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_accept(p_group uuid, p_max int) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_person_scope();
  v_by uuid;
  v_left timestamptz;
  v_found boolean;
BEGIN
  IF v_me IS NULL OR taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'an invite is answered by its person in their own transaction' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = v_me AND p.active AND NOT p.deleted) THEN
    RAISE EXCEPTION 'the account is not active' USING ERRCODE = '42501';
  END IF;
  PERFORM 1 FROM people_groups g WHERE g.id = p_group FOR UPDATE;
  SELECT i.invited_by INTO v_by FROM group_invites i WHERE i.group_id = p_group AND i.person_id = v_me AND NOT i.hidden;
  IF NOT FOUND THEN
    RETURN 'none';
  END IF;
  SELECT m.left_at INTO v_left FROM group_members m WHERE m.group_id = p_group AND m.person_id = v_me;
  v_found := FOUND;
  IF v_found AND v_left IS NULL THEN
    DELETE FROM group_invites i WHERE i.group_id = p_group AND i.person_id = v_me;
    RETURN 'member';
  END IF;
  IF (SELECT count(*) FROM group_members m WHERE m.group_id = p_group AND m.left_at IS NULL) >= p_max THEN
    RETURN 'full';
  END IF;
  IF v_found THEN
    UPDATE group_members m SET left_at = NULL, left_how = NULL, removed_by = NULL, how = 'invite', added_by = v_by, role = 'member',
           joined_at = clock_timestamp(), hidden_at = NULL, visible = m.visible + tstzmultirange(tstzrange(clock_timestamp(), NULL))
     WHERE m.group_id = p_group AND m.person_id = v_me;
  ELSE
    INSERT INTO group_members (group_id, person_id, how, added_by, visible)
    VALUES (p_group, v_me, 'invite', v_by, tstzmultirange(tstzrange(clock_timestamp(), NULL)));
  END IF;
  DELETE FROM group_invites i WHERE i.group_id = p_group AND i.person_id = v_me;
  RETURN 'joined';
END
$$;
-- +goose StatementEnd

-- An admin adding people (A3, A8): for each of up to 256 people, whether
-- the account is active, whether their "Who can add me to groups" lets the
-- admin in scope add them directly, and whether a block stands between
-- them either way. Nothing else of them.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_may_add(p_people uuid[])
RETURNS TABLE (person_id uuid, active boolean, allowed boolean, blocked boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_person_scope();
BEGIN
  IF v_me IS NULL OR taskiem_person_business() IS NOT NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  IF cardinality(p_people) > 256 THEN
    RAISE EXCEPTION 'at most 256 people at once' USING ERRCODE = '22023';
  END IF;
  RETURN QUERY
  SELECT p.person_id, p.active AND NOT p.deleted,
         taskiem_people_allows(COALESCE(s.groups_add, 'contacts'), p.person_id, v_me),
         EXISTS (SELECT 1 FROM people_blocks b WHERE (b.person_id = p.person_id AND b.blocked_id = v_me) OR (b.person_id = v_me AND b.blocked_id = p.person_id))
    FROM person_profiles p LEFT JOIN person_privacy s ON s.person_id = p.person_id
   WHERE p.person_id = ANY (p_people) AND p.person_id <> v_me;
END
$$;
-- +goose StatementEnd

-- The numbers of the group's current members who chose to show theirs
-- (§5.4, §17 Q5), to a current member in scope, never across a block
-- either way.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_phones(p_group uuid)
RETURNS TABLE (person_id uuid, number text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_person_scope();
BEGIN
  IF v_me IS NULL OR taskiem_person_business() IS NOT NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = p_group AND m.person_id = v_me AND m.left_at IS NULL) THEN
    RETURN;
  END IF;
  RETURN QUERY
  SELECT m.person_id, ph.number
    FROM group_members m
    JOIN person_privacy s ON s.person_id = m.person_id AND s.show_number
    JOIN person_profiles f ON f.person_id = m.person_id AND f.active AND NOT f.deleted
    JOIN user_phones ph ON ph.user_id = m.person_id
   WHERE m.group_id = p_group AND m.left_at IS NULL AND m.person_id <> v_me
     AND NOT EXISTS (SELECT 1 FROM people_blocks b WHERE (b.person_id = m.person_id AND b.blocked_id = v_me) OR (b.person_id = v_me AND b.blocked_id = m.person_id));
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_group_member(uuid)',
                           'taskiem_group_admin(uuid)',
                           'taskiem_group_invited(uuid)',
                           'taskiem_group_create(uuid, bytea, text, text)',
                           'taskiem_group_link_find(bytea)',
                           'taskiem_group_link_photo(bytea)',
                           'taskiem_group_join(bytea, integer, integer)',
                           'taskiem_group_accept(uuid, integer)',
                           'taskiem_group_may_add(uuid[])',
                           'taskiem_group_phones(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_group_member_guard() FROM PUBLIC;

-- +goose Down
DROP FUNCTION taskiem_group_phones(uuid);
DROP FUNCTION taskiem_group_may_add(uuid[]);
DROP FUNCTION taskiem_group_accept(uuid, integer);
DROP FUNCTION taskiem_group_join(bytea, integer, integer);
DROP FUNCTION taskiem_group_link_photo(bytea);
DROP FUNCTION taskiem_group_link_find(bytea);
DROP FUNCTION taskiem_group_create(uuid, bytea, text, text);
DROP TABLE group_search;
DROP TABLE group_typing;
DROP TABLE group_photos;
DROP TABLE group_links;
DROP TABLE group_join_requests;
DROP TABLE group_invites;
DROP TABLE group_reads;
DROP TABLE group_reactions;
DROP TABLE group_marks;
DROP TABLE group_files;
DROP TABLE group_messages;
-- The group's own policies name its members: they go with them.
DROP TABLE group_members CASCADE;
DROP TABLE group_keys;
DROP TABLE people_groups;
DROP FUNCTION taskiem_group_member_guard();
DROP FUNCTION taskiem_group_invited(uuid);
DROP FUNCTION taskiem_group_admin(uuid);
DROP FUNCTION taskiem_group_member(uuid);
DELETE FROM push_outbox WHERE kind IN ('group', 'group_invite');
ALTER TABLE push_outbox
  DROP CONSTRAINT push_outbox_side,
  DROP CONSTRAINT push_outbox_kind_check,
  ADD CONSTRAINT push_outbox_kind_check CHECK (kind IN ('business', 'customers', 'orders', 'approvals', 'alerts', 'you', 'person', 'request')),
  ADD CONSTRAINT push_outbox_side CHECK ((tenant_id IS NULL) = (kind IN ('you', 'person', 'request')));
ALTER TABLE person_notify DROP COLUMN groups;
REVOKE SELECT (groups_add) ON person_privacy FROM taskiem_dispatch;
ALTER TABLE person_privacy DROP COLUMN groups_add;
