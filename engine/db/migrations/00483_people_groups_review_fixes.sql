-- People chatting with people, stage 5b: the review's fixes to groups on
-- the server (docs/spec/people-chatting.md, "As built: stage 5b, groups
-- (server)", "Review fixes"; docs/spec/people-chatting-5b.md A3 to A8).
--
-- * Join requests do not outlive the person's joining or leaving: a link
--   join, an accepted invite or an admin's add takes the person's request
--   away (the API does so too when they leave), and the person may take
--   their own back.
-- * Someone an admin removed stays known as removed (group_removed) after
--   they delete their copy: their link join waits for an admin. An
--   admin's add, an accepted invite or an approval clears it.
-- * A link join takes the group's row first, then reads the link: a link
--   turned off or replaced at that moment is gone for the join.
-- * The public preview of a link no longer says whether joining waits for
--   an admin (taskiem_group_link_find loses approve_joins): a person an
--   admin blocked learns nothing from it.
-- * Someone who left sees no reaction, read or delivered mark made after
--   they left (the policies of group_reactions and group_reads).
-- * Deleting an account takes away every invite to the person, the ones
--   they were never shown too (taskiem_group_forget).
-- * Row-level security holds to the API's rules, not only "who": the
--   guard of group_members applies the own-row rules to admins too (an
--   admin's bypass is for other people's rows, and only to add, remove,
--   re-add or change a role); once left, what a person sees ends at
--   left_at; how they left and who removed them stay. people_groups is
--   changed by its admins (settings), its editors (info, photo, A2) and
--   any member (a new message's time), and deleted only by its last
--   member; group_photos by its editors; group_files and group_search by
--   the message's sender.
-- * group_members.marks_at moves only with a member's read or delivered
--   marks or their time in the group, so the live connection's ticks wake
--   for those alone (not for another member's mute, archive or mentions).
-- * taskiem_group_push: a group message's pushes in one statement, after
--   the send's commit, once per message (group_pushed).
-- +goose Up

-- --- removed, and kept so ---

-- Someone an admin removed from the group, kept after they delete their
-- copy, until an admin lets them back in (an add, an approval, an invite
-- they accept). Gone with the group, and with their account.
CREATE TABLE group_removed (
  group_id    uuid NOT NULL REFERENCES people_groups(id) ON DELETE CASCADE,
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  removed_by  uuid,
  removed_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (group_id, person_id)
);
CREATE INDEX group_removed_person ON group_removed (person_id);
INSERT INTO group_removed (group_id, person_id, removed_by, removed_at)
SELECT group_id, person_id, removed_by, left_at FROM group_members WHERE left_how = 'removed';
ALTER TABLE group_removed ENABLE ROW LEVEL SECURITY;
ALTER TABLE group_removed FORCE ROW LEVEL SECURITY;
REVOKE ALL ON group_removed FROM PUBLIC;
CREATE POLICY admin_read ON group_removed FOR SELECT TO taskiem_app USING (taskiem_group_admin(group_id));
CREATE POLICY admin_insert ON group_removed FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_group_admin(group_id) AND removed_by = taskiem_person_scope() AND person_id <> taskiem_person_scope());
CREATE POLICY admin_delete ON group_removed FOR DELETE TO taskiem_app USING (taskiem_group_admin(group_id));
GRANT SELECT, INSERT, DELETE ON group_removed TO taskiem_app;
CREATE POLICY dispatch_all ON group_removed TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (group_id, person_id), DELETE ON group_removed TO taskiem_dispatch;

-- The definers take a person's join request away when they join.
GRANT DELETE ON group_join_requests TO taskiem_dispatch;

-- --- the ticks' own time ---

ALTER TABLE group_members ADD COLUMN marks_at timestamptz NOT NULL DEFAULT clock_timestamp();
CREATE INDEX group_members_marks ON group_members (group_id, marks_at);

-- marks_at moves with a member's read or delivered mark, or their time in
-- the group (what another member's ticks are made of), and nothing else.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_marks_changed() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.read_upto IS DISTINCT FROM OLD.read_upto OR NEW.delivered_upto IS DISTINCT FROM OLD.delivered_upto
     OR NEW.left_at IS DISTINCT FROM OLD.left_at OR NEW.visible IS DISTINCT FROM OLD.visible THEN
    NEW.marks_at := clock_timestamp();
  ELSE
    NEW.marks_at := OLD.marks_at;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER group_members_marks BEFORE UPDATE ON group_members FOR EACH ROW EXECUTE FUNCTION taskiem_group_marks_changed();
REVOKE EXECUTE ON FUNCTION taskiem_group_marks_changed() FROM PUBLIC;

-- --- memberships: the API's rules, by row-level security too ---

-- As the API (taskiem_app, the person in scope): one's own row, admin or
-- not, changes only in one's own settings and marks, and by leaving (left
-- now, seeing nothing from then); an admin's role may be given up. Another
-- person's row is an admin's only: to add them (from now on), remove them
-- (seeing nothing from then), let them back (from now on) or change their
-- role while they are in the group; nothing of their own settings. Once
-- left, the leaving stays as it was (when, how, by whom). The definers
-- (taskiem_dispatch) join, accept and make groups.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_group_member_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid;
  v_admin boolean;
BEGIN
  IF current_user <> 'taskiem_app' THEN
    RETURN NEW;
  END IF;
  v_me := taskiem_person_scope();
  v_admin := taskiem_group_admin(NEW.group_id);
  IF TG_OP = 'INSERT' THEN
    IF NOT v_admin OR NEW.person_id = v_me THEN
      RAISE EXCEPTION 'only an admin adds people to a group' USING ERRCODE = '42501';
    END IF;
    IF NEW.role <> 'member' OR NEW.how NOT IN ('added', 'link') OR NEW.added_by IS DISTINCT FROM v_me
       OR NEW.left_at IS NOT NULL OR NEW.removed_by IS NOT NULL OR NEW.left_sealed IS NOT NULL OR NEW.hidden_at IS NOT NULL
       OR NEW.joined_at < now() OR NOT (NEW.visible <@ tstzmultirange(tstzrange(now(), NULL))) THEN
      RAISE EXCEPTION 'someone added joins as a member from now' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.person_id <> OLD.person_id OR NEW.group_id <> OLD.group_id THEN
    RAISE EXCEPTION 'a membership stays whose it is' USING ERRCODE = '42501';
  END IF;
  -- Once left, the leaving stays as it was until they are let back in.
  IF OLD.left_at IS NOT NULL AND NEW.left_at IS NOT NULL
     AND (NEW.left_at <> OLD.left_at OR NEW.left_how IS DISTINCT FROM OLD.left_how OR NEW.removed_by IS DISTINCT FROM OLD.removed_by
          OR NEW.left_sealed IS DISTINCT FROM OLD.left_sealed) THEN
    RAISE EXCEPTION 'how someone left stays' USING ERRCODE = '42501';
  END IF;
  -- Left: nothing from then on is seen.
  IF NEW.left_at IS NOT NULL AND NOT (NEW.visible <@ (OLD.visible - tstzmultirange(tstzrange(NEW.left_at, NULL)))) THEN
    RAISE EXCEPTION 'someone who left sees nothing after' USING ERRCODE = '42501';
  END IF;
  IF NEW.person_id = v_me THEN
    IF (NEW.role <> OLD.role AND NOT (v_admin AND OLD.role = 'admin' AND NEW.role = 'member' AND NEW.left_at IS NULL))
       OR NEW.how <> OLD.how OR NEW.added_by IS DISTINCT FROM OLD.added_by OR NEW.joined_at <> OLD.joined_at
       OR NEW.removed_by IS DISTINCT FROM OLD.removed_by
       OR (OLD.left_at IS NOT NULL AND NEW.left_at IS DISTINCT FROM OLD.left_at)
       OR (OLD.left_at IS NULL AND NEW.left_at IS NOT NULL AND (NEW.left_how <> 'left' OR NEW.left_at < now()
           OR NEW.left_sealed IS DISTINCT FROM (SELECT g.sealed FROM people_groups g WHERE g.id = NEW.group_id)))
       OR (NEW.left_at IS NULL AND NEW.left_sealed IS DISTINCT FROM OLD.left_sealed)
       OR NOT (NEW.visible <@ OLD.visible) THEN
      RAISE EXCEPTION 'a member changes only their own settings' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
  END IF;
  IF NOT v_admin THEN
    RAISE EXCEPTION 'only an admin changes another''s membership' USING ERRCODE = '42501';
  END IF;
  -- Their own settings and marks stay theirs (let back in, the chat shows
  -- again).
  IF NEW.muted <> OLD.muted OR NEW.archived <> OLD.archived OR NEW.mentions <> OLD.mentions
     OR NEW.read_upto <> OLD.read_upto OR NEW.delivered_upto <> OLD.delivered_upto
     OR (NEW.hidden_at IS DISTINCT FROM OLD.hidden_at AND NOT (OLD.left_at IS NOT NULL AND NEW.left_at IS NULL AND NEW.hidden_at IS NULL)) THEN
    RAISE EXCEPTION 'a member''s own settings are theirs' USING ERRCODE = '42501';
  END IF;
  IF OLD.left_at IS NULL AND NEW.left_at IS NULL THEN
    -- In the group: only their role.
    IF NEW.how <> OLD.how OR NEW.added_by IS DISTINCT FROM OLD.added_by OR NEW.joined_at <> OLD.joined_at
       OR NEW.visible <> OLD.visible OR NEW.removed_by IS NOT NULL OR NEW.left_sealed IS NOT NULL THEN
      RAISE EXCEPTION 'an admin changes only a member''s role' USING ERRCODE = '42501';
    END IF;
  ELSIF OLD.left_at IS NULL THEN
    -- Removed now, by this admin.
    IF NEW.left_how <> 'removed' OR NEW.removed_by IS DISTINCT FROM v_me OR NEW.left_at < now() OR NEW.role <> OLD.role
       OR NEW.how <> OLD.how OR NEW.added_by IS DISTINCT FROM OLD.added_by OR NEW.joined_at <> OLD.joined_at
       OR NEW.left_sealed IS DISTINCT FROM (SELECT g.sealed FROM people_groups g WHERE g.id = NEW.group_id) THEN
      RAISE EXCEPTION 'an admin removes someone now' USING ERRCODE = '42501';
    END IF;
  ELSIF NEW.left_at IS NULL THEN
    -- Let back in, by this admin, from now on.
    IF NEW.role <> 'member' OR NEW.how NOT IN ('added', 'link') OR NEW.added_by IS DISTINCT FROM v_me
       OR NEW.removed_by IS NOT NULL OR NEW.left_sealed IS NOT NULL OR NEW.joined_at < now()
       OR NOT (NEW.visible <@ (OLD.visible + tstzmultirange(tstzrange(now(), NULL)))) THEN
      RAISE EXCEPTION 'someone let back in joins from now' USING ERRCODE = '42501';
    END IF;
  ELSE
    -- Left, and staying so: nothing of theirs.
    IF NEW.role <> OLD.role OR NEW.how <> OLD.how OR NEW.added_by IS DISTINCT FROM OLD.added_by OR NEW.joined_at <> OLD.joined_at
       OR NEW.visible <> OLD.visible THEN
      RAISE EXCEPTION 'an admin has nothing to change of someone who left' USING ERRCODE = '42501';
    END IF;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- --- the group's own row ---

-- As the API: its settings by an admin; its name, description and photo
-- by an admin, or any member while admins_only_edit is off (A2); its last
-- message's time by any member, only forward; nothing else.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  v_admin boolean;
BEGIN
  IF current_user <> 'taskiem_app' THEN
    RETURN NEW;
  END IF;
  v_admin := taskiem_group_admin(OLD.id);
  IF NEW.id <> OLD.id OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at <> OLD.created_at
     OR NEW.last_message_at < OLD.last_message_at OR NEW.last_message_at > clock_timestamp() THEN
    RAISE EXCEPTION 'a group''s own row stays' USING ERRCODE = '42501';
  END IF;
  IF NOT v_admin AND (NEW.admins_only_post <> OLD.admins_only_post OR NEW.admins_only_edit <> OLD.admins_only_edit
                      OR NEW.approve_joins <> OLD.approve_joins) THEN
    RAISE EXCEPTION 'only an admin changes a group''s settings' USING ERRCODE = '42501';
  END IF;
  IF NOT v_admin AND OLD.admins_only_edit AND (NEW.sealed <> OLD.sealed OR NEW.photo_at IS DISTINCT FROM OLD.photo_at) THEN
    RAISE EXCEPTION 'only an admin changes this group''s info' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER people_groups_guard BEFORE UPDATE ON people_groups FOR EACH ROW EXECUTE FUNCTION taskiem_group_guard();
REVOKE EXECUTE ON FUNCTION taskiem_group_guard() FROM PUBLIC;

-- Deleted only by its last member (closing it as they leave, §14).
DROP POLICY member_delete ON people_groups;
CREATE POLICY last_member_delete ON people_groups FOR DELETE TO taskiem_app
  USING (taskiem_person_business() IS NULL
    AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = people_groups.id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL)
    AND NOT EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = people_groups.id AND m.person_id <> taskiem_person_scope() AND m.left_at IS NULL));

-- The photo: by the group's editors (A2).
DROP POLICY member_write ON group_photos;
-- +goose StatementBegin
DO $$
DECLARE
  editor text := 'taskiem_person_business() IS NULL AND (taskiem_group_admin(group_id) OR EXISTS (SELECT 1 FROM people_groups g
    JOIN group_members m ON m.group_id = g.id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL
    WHERE g.id = group_photos.group_id AND NOT g.admins_only_edit))';
BEGIN
  EXECUTE 'CREATE POLICY editor_insert ON group_photos FOR INSERT TO taskiem_app WITH CHECK (' || editor || ')';
  EXECUTE 'CREATE POLICY editor_update ON group_photos FOR UPDATE TO taskiem_app USING (' || editor || ') WITH CHECK (' || editor || ')';
  EXECUTE 'CREATE POLICY editor_delete ON group_photos FOR DELETE TO taskiem_app USING (' || editor || ')';
END
$$;
-- +goose StatementEnd

-- A message's file and search tokens: written and taken away by its
-- sender, while in the group.
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['group_files', 'group_search'] LOOP
    EXECUTE format('DROP POLICY member_insert ON %I', t);
    EXECUTE format('DROP POLICY member_delete ON %I', t);
    EXECUTE format('CREATE POLICY sender_insert ON %I FOR INSERT TO taskiem_app
      WITH CHECK (taskiem_person_business() IS NULL
        AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = %I.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL)
        AND EXISTS (SELECT 1 FROM group_messages x WHERE x.group_id = %I.group_id AND x.id = %I.message_id AND x.sender_id = taskiem_person_scope()
                      AND x.kind <> ''event'' AND x.deleted_at IS NULL %s))', t, t, t, t,
      CASE WHEN t = 'group_files' THEN 'AND x.attachment IS NOT NULL' ELSE '' END);
    EXECUTE format('CREATE POLICY sender_delete ON %I FOR DELETE TO taskiem_app
      USING (taskiem_person_business() IS NULL
        AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = %I.group_id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL)
        AND EXISTS (SELECT 1 FROM group_messages x WHERE x.group_id = %I.group_id AND x.id = %I.message_id AND x.sender_id = taskiem_person_scope()))', t, t, t, t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- Someone who left sees no reaction, read or delivered mark made after.
DROP POLICY member_read ON group_reactions;
CREATE POLICY member_read ON group_reactions FOR SELECT TO taskiem_app
  USING (EXISTS (SELECT 1 FROM group_messages g WHERE g.group_id = group_reactions.group_id AND g.id = group_reactions.message_id)
    AND NOT EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_reactions.group_id AND m.person_id = taskiem_person_scope()
                      AND m.left_at < group_reactions.at));
DROP POLICY member_read ON group_reads;
CREATE POLICY member_read ON group_reads FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_group_member(group_id)
    AND NOT EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_reads.group_id AND m.person_id = taskiem_person_scope()
                      AND m.left_at < group_reads.at));

-- --- the definers ---

-- The link's preview: nothing of whether joining waits for an admin.
DROP FUNCTION taskiem_group_link_find(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_link_find(p_hash bytea)
RETURNS TABLE (group_id uuid, replaced_at timestamptz, sealed bytea, key_sealed text, kms_key text, members int, photo_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.group_id, l.replaced_at, g.sealed, k.sealed, k.kms_key,
         (SELECT count(*)::int FROM group_members m WHERE m.group_id = g.id AND m.left_at IS NULL),
         g.photo_at
    FROM group_links l JOIN people_groups g ON g.id = l.group_id JOIN group_keys k ON k.group_id = g.id
   WHERE l.code_hash = p_hash AND length(p_hash) = 32
$$;
-- +goose StatementEnd

-- Joining a group by its live link, as the person in scope (A4): joined at
-- once; or waiting for an admin when the group approves new members, or
-- when an admin removed them before (group_removed, kept after they
-- delete their copy) or blocked them (state waiting, the same either way);
-- member when they are in it; full at p_max members; busy past p_per_hour
-- link joins in the last hour; gone for a replaced or turned off link;
-- none for no such link. The group's row is taken before the link is read,
-- so a link turned off or replaced at that moment is gone. Joined, any
-- request of theirs to join goes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_group_join(p_hash bytea, p_max int, p_per_hour int)
RETURNS TABLE (group_id uuid, state text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_person_scope();
  v_group uuid;
  v_replaced timestamptz;
  v_approve boolean;
  v_left timestamptz;
  v_found boolean;
BEGIN
  IF v_me IS NULL OR taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'a group is joined by a person in their own transaction' USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = v_me AND p.active AND NOT p.deleted) THEN
    RAISE EXCEPTION 'the account is not active' USING ERRCODE = '42501';
  END IF;
  SELECT l.group_id INTO v_group FROM group_links l WHERE l.code_hash = p_hash;
  IF NOT FOUND THEN
    RETURN QUERY SELECT NULL::uuid, 'none'::text;
    RETURN;
  END IF;
  -- The group's row is every writer's first lock (the API's order); the
  -- link is read after it, as it is now.
  SELECT g.approve_joins INTO v_approve FROM people_groups g WHERE g.id = v_group FOR UPDATE;
  SELECT l.replaced_at INTO v_replaced FROM group_links l WHERE l.code_hash = p_hash;
  IF NOT FOUND OR v_replaced IS NOT NULL THEN
    RETURN QUERY SELECT v_group, 'gone'::text;
    RETURN;
  END IF;
  SELECT m.left_at INTO v_left FROM group_members m WHERE m.group_id = v_group AND m.person_id = v_me;
  v_found := FOUND;
  IF v_found AND v_left IS NULL THEN
    DELETE FROM group_join_requests r WHERE r.group_id = v_group AND r.person_id = v_me;
    RETURN QUERY SELECT v_group, 'member'::text;
    RETURN;
  END IF;
  IF (SELECT count(*) FROM group_members m WHERE m.group_id = v_group AND m.left_at IS NULL) >= p_max THEN
    RETURN QUERY SELECT v_group, 'full'::text;
    RETURN;
  END IF;
  IF v_approve OR EXISTS (SELECT 1 FROM group_removed r WHERE r.group_id = v_group AND r.person_id = v_me)
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
    UPDATE group_members m SET left_at = NULL, left_how = NULL, removed_by = NULL, left_sealed = NULL, how = 'link', added_by = NULL, role = 'member',
           joined_at = clock_timestamp(), hidden_at = NULL, visible = m.visible + tstzmultirange(tstzrange(clock_timestamp(), NULL))
     WHERE m.group_id = v_group AND m.person_id = v_me;
  ELSE
    INSERT INTO group_members (group_id, person_id, how, visible) VALUES (v_group, v_me, 'link', tstzmultirange(tstzrange(clock_timestamp(), NULL)));
  END IF;
  DELETE FROM group_invites i WHERE i.group_id = v_group AND i.person_id = v_me;
  DELETE FROM group_join_requests r WHERE r.group_id = v_group AND r.person_id = v_me;
  RETURN QUERY SELECT v_group, 'joined'::text;
END
$$;
-- +goose StatementEnd

-- Accepting an invite the person in scope may see (A3): they join the
-- group from now (an admin's invite: no longer kept as removed, and any
-- request of theirs to join goes). full at p_max members; none without
-- such an invite.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_group_accept(p_group uuid, p_max int) RETURNS text
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
    DELETE FROM group_join_requests r WHERE r.group_id = p_group AND r.person_id = v_me;
    RETURN 'member';
  END IF;
  IF (SELECT count(*) FROM group_members m WHERE m.group_id = p_group AND m.left_at IS NULL) >= p_max THEN
    RETURN 'full';
  END IF;
  IF v_found THEN
    UPDATE group_members m SET left_at = NULL, left_how = NULL, removed_by = NULL, left_sealed = NULL, how = 'invite', added_by = v_by, role = 'member',
           joined_at = clock_timestamp(), hidden_at = NULL, visible = m.visible + tstzmultirange(tstzrange(clock_timestamp(), NULL))
     WHERE m.group_id = p_group AND m.person_id = v_me;
  ELSE
    INSERT INTO group_members (group_id, person_id, how, added_by, visible)
    VALUES (p_group, v_me, 'invite', v_by, tstzmultirange(tstzrange(clock_timestamp(), NULL)));
  END IF;
  DELETE FROM group_invites i WHERE i.group_id = p_group AND i.person_id = v_me;
  DELETE FROM group_join_requests r WHERE r.group_id = p_group AND r.person_id = v_me;
  DELETE FROM group_removed r WHERE r.group_id = p_group AND r.person_id = v_me;
  RETURN 'joined';
END
$$;
-- +goose StatementEnd

-- Deleting an account (§14): every invite to the person goes, the ones
-- they were never shown (an add across a block, A8) too, with their
-- requests to join and their being kept as removed. Only in the deletion's
-- own transaction: once taskiem_person_delete has revoked every session of
-- theirs.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_forget() RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_person_scope();
BEGIN
  IF v_me IS NULL OR taskiem_person_business() IS NOT NULL THEN
    RAISE EXCEPTION 'a person forgets only themself' USING ERRCODE = '42501';
  END IF;
  IF EXISTS (SELECT 1 FROM sessions s WHERE s.user_id = v_me AND s.revoked_at IS NULL) THEN
    RAISE EXCEPTION 'only while the account is being deleted' USING ERRCODE = '42501';
  END IF;
  DELETE FROM group_invites i WHERE i.person_id = v_me;
  DELETE FROM group_join_requests r WHERE r.person_id = v_me;
  DELETE FROM group_removed r WHERE r.person_id = v_me;
END
$$;
-- +goose StatementEnd

-- The pushes of a group message (§13), once per message, in one statement:
-- the message's sender in scope, within ten minutes of sending it; to each
-- member now in the group who sees it, not muting it (or mentioned in it
-- with mention pushes on, A7), with pushes of groups on (person_notify),
-- neither blocking the other, and with a device of their own that takes
-- pushes for You now (p_kinds: the kinds of device this server sends to).
-- The title (the group's name) and words are the server's. Answers how
-- many were queued.
CREATE TABLE group_pushed (
  group_id    uuid NOT NULL,
  message_id  uuid NOT NULL,
  at          timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (group_id, message_id),
  FOREIGN KEY (group_id, message_id) REFERENCES group_messages (group_id, id) ON DELETE CASCADE
);
ALTER TABLE group_pushed ENABLE ROW LEVEL SECURITY;
ALTER TABLE group_pushed FORCE ROW LEVEL SECURITY;
REVOKE ALL ON group_pushed FROM PUBLIC;
CREATE POLICY dispatch_all ON group_pushed TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT INSERT ON group_pushed TO taskiem_dispatch;
-- Seen by the message's sender alone; written only by the definer.
CREATE POLICY sender_read ON group_pushed FOR SELECT TO taskiem_app
  USING (EXISTS (SELECT 1 FROM group_messages x WHERE x.group_id = group_pushed.group_id AND x.id = group_pushed.message_id
                   AND x.sender_id = taskiem_person_scope()));
GRANT SELECT ON group_pushed TO taskiem_app;
CREATE POLICY dispatch_read ON group_messages FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (group_id, id, sender_id, kind, created_at, mentions) ON group_messages TO taskiem_dispatch;
GRANT SELECT (muted, mentions) ON group_members TO taskiem_dispatch;
CREATE POLICY dispatch_read ON person_notify FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (person_id, groups) ON person_notify TO taskiem_dispatch;
GRANT SELECT (kind, kinds, failed_since, unreached_since, device_id) ON push_subscriptions TO taskiem_dispatch;
GRANT DELETE ON push_outbox TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_push(p_group uuid, p_message uuid, p_kinds text[], p_title text, p_body text, p_body_mentioned text)
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_person_scope();
  v_at timestamptz;
  v_mentions uuid[];
  n int;
BEGIN
  IF v_me IS NULL OR taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'a group''s push is its sender''s' USING ERRCODE = '42501';
  END IF;
  SELECT x.created_at, x.mentions INTO v_at, v_mentions FROM group_messages x
   WHERE x.group_id = p_group AND x.id = p_message AND x.sender_id = v_me AND x.kind <> 'event' AND x.created_at > now() - interval '10 minutes';
  IF NOT FOUND THEN
    RETURN 0;
  END IF;
  INSERT INTO group_pushed (group_id, message_id) VALUES (p_group, p_message) ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n = 0 THEN
    RETURN 0;
  END IF;
  WITH rcpt AS (
    SELECT m.person_id, m.person_id = ANY (v_mentions) AS tagged
      FROM group_members m
     WHERE m.group_id = p_group AND m.left_at IS NULL AND m.person_id <> v_me AND m.visible @> v_at
       AND (NOT m.muted OR (m.mentions AND m.person_id = ANY (v_mentions)))
       AND COALESCE((SELECT o.groups FROM person_notify o WHERE o.person_id = m.person_id), true)
       AND NOT EXISTS (SELECT 1 FROM people_blocks b WHERE (b.person_id = m.person_id AND b.blocked_id = v_me) OR (b.person_id = v_me AND b.blocked_id = m.person_id))
       AND EXISTS (SELECT 1 FROM push_subscriptions s WHERE s.tenant_id IS NULL AND s.user_id = m.person_id AND 'you' = ANY (s.kinds)
                     AND s.kind = ANY (p_kinds) AND s.failed_since IS NULL AND s.unreached_since IS NULL
                     AND (s.device_id IS NULL OR taskiem_push_device_live(s.user_id, s.device_id)))
  ), older AS (
    DELETE FROM push_outbox o USING rcpt r WHERE o.tenant_id IS NULL AND o.user_id = r.person_id AND o.tag = 'group:' || p_group::text
  )
  INSERT INTO push_outbox (id, tenant_id, user_id, kind, title, body, link, tag)
  SELECT gen_random_uuid(), NULL, r.person_id, 'group', left(p_title, 200), left(CASE WHEN r.tagged THEN p_body_mentioned ELSE p_body END, 1000),
         '/you/groups/' || p_group::text, 'group:' || p_group::text
    FROM rcpt r;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n > 0 THEN
    PERFORM pg_notify('push_outbox', '');
  END IF;
  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_group_link_find(bytea)',
                           'taskiem_group_forget()',
                           'taskiem_group_push(uuid, uuid, text[], text, text, text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_group_push(uuid, uuid, text[], text, text, text);
DROP FUNCTION taskiem_group_forget();
DROP TABLE group_pushed;
REVOKE DELETE ON push_outbox FROM taskiem_dispatch;
REVOKE SELECT (kind, kinds, failed_since, unreached_since, device_id) ON push_subscriptions FROM taskiem_dispatch;
REVOKE SELECT (person_id, groups) ON person_notify FROM taskiem_dispatch;
DROP POLICY dispatch_read ON person_notify;
REVOKE SELECT (muted, mentions) ON group_members FROM taskiem_dispatch;
REVOKE SELECT (group_id, id, sender_id, kind, created_at, mentions) ON group_messages FROM taskiem_dispatch;
DROP POLICY dispatch_read ON group_messages;

DROP POLICY member_read ON group_reads;
CREATE POLICY member_read ON group_reads FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND taskiem_group_member(group_id));
DROP POLICY member_read ON group_reactions;
CREATE POLICY member_read ON group_reactions FOR SELECT TO taskiem_app
  USING (EXISTS (SELECT 1 FROM group_messages g WHERE g.group_id = group_reactions.group_id AND g.id = group_reactions.message_id));

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['group_files', 'group_search'] LOOP
    EXECUTE format('DROP POLICY sender_insert ON %I', t);
    EXECUTE format('DROP POLICY sender_delete ON %I', t);
    EXECUTE format('CREATE POLICY member_insert ON %I FOR INSERT TO taskiem_app
      WITH CHECK (taskiem_person_business() IS NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = %I.group_id
        AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL))', t, t);
    EXECUTE format('CREATE POLICY member_delete ON %I FOR DELETE TO taskiem_app
      USING (taskiem_person_business() IS NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = %I.group_id
        AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL))', t, t);
  END LOOP;
END
$$;
-- +goose StatementEnd

DROP POLICY editor_insert ON group_photos;
DROP POLICY editor_update ON group_photos;
DROP POLICY editor_delete ON group_photos;
CREATE POLICY member_write ON group_photos FOR ALL TO taskiem_app
  USING (taskiem_person_business() IS NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_photos.group_id
    AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL))
  WITH CHECK (taskiem_person_business() IS NULL AND EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = group_photos.group_id
    AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL));

DROP POLICY last_member_delete ON people_groups;
CREATE POLICY member_delete ON people_groups FOR DELETE TO taskiem_app
  USING (EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = people_groups.id AND m.person_id = taskiem_person_scope() AND m.left_at IS NULL));
DROP TRIGGER people_groups_guard ON people_groups;
DROP FUNCTION taskiem_group_guard();

DROP TRIGGER group_members_marks ON group_members;
DROP FUNCTION taskiem_group_marks_changed();
DROP INDEX group_members_marks;
ALTER TABLE group_members DROP COLUMN marks_at;

REVOKE DELETE ON group_join_requests FROM taskiem_dispatch;
DROP TABLE group_removed;

-- 00482's definers and guard.
DROP FUNCTION taskiem_group_link_find(bytea);
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
ALTER FUNCTION taskiem_group_link_find(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_group_link_find(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_group_link_find(bytea) TO taskiem_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_group_join(p_hash bytea, p_max int, p_per_hour int)
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
    UPDATE group_members m SET left_at = NULL, left_how = NULL, removed_by = NULL, left_sealed = NULL, how = 'link', added_by = NULL, role = 'member',
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_group_accept(p_group uuid, p_max int) RETURNS text
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
    UPDATE group_members m SET left_at = NULL, left_how = NULL, removed_by = NULL, left_sealed = NULL, how = 'invite', added_by = v_by, role = 'member',
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_group_member_guard() RETURNS trigger
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
