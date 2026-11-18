-- People chatting with people, stage 5b: safety in groups
-- (docs/spec/people-chatting.md §8.2 to §8.5, §14; people-chatting-5b.md B
-- with the founder's B1; decision 0069 points 3 and 11). "As built: stage
-- 5b, group safety (server)".
--
-- * Limits: group_ledger keeps what a person did with groups (made one,
--   invited someone who is not their contact, joined or asked to join by a
--   link), counted for their limits (peoplesafety.GroupAllowance); only
--   they read and add to it, and the safety job's sweep prunes it.
-- * Reports of a group or a member: people_reports.group_id (the group,
--   kept after it is gone) and group_sealed (its name and description at
--   report time, sealed under the report's own key); reported_id is the
--   member reported, or NULL for the group. The evidence is the last 20
--   messages the reporter could see, sealed as 5a's reports are.
-- * Moderators act on a group: people_moderation.group_id with the actions
--   group_link_off and group_close (person_id NULL for those). What is in
--   force is kept on group_standing (members read it; only taskiem_safety
--   writes it), with the members told by an event message from Taskiem. A
--   closure deletes the group (§14) 30 days on, unless its admins' appeal
--   (people_appeals, one an action, by any admin) is upheld.
-- * Scam checks in groups (B1): a held group message is kept in people_held
--   with group_id (one copy: held for the whole group) and its mentions;
--   group_messages.screen marks a warned one; people_screen_verdicts keeps
--   only the verdict, with the group.
-- * Lawful requests: lawful_requests.scope_groups (none, sent: the person's
--   own group messages, seen: every group message they could see).
--
-- Every new moderators' function is a definer owned by taskiem_safety,
-- refused unless the platform chain is in scope (taskiem_moderator_check).
-- +goose Up

-- --- limits ---

CREATE TABLE group_ledger (
  person_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       text NOT NULL CHECK (kind IN ('create', 'invite', 'link_join')),
  group_id   uuid,
  at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX group_ledger_person ON group_ledger (person_id, kind, at);
CREATE INDEX group_ledger_at ON group_ledger (at);
ALTER TABLE group_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE group_ledger FORCE ROW LEVEL SECURITY;
REVOKE ALL ON group_ledger FROM PUBLIC;
-- The person's own, read and added to; never changed or taken back by
-- them (that would undo their limits).
CREATE POLICY own_read ON group_ledger FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
CREATE POLICY own_insert ON group_ledger FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope() AND at > now() - interval '1 minute');
GRANT SELECT, INSERT ON group_ledger TO taskiem_app;
-- The sweep and an account's deletion (definers owned by taskiem_dispatch).
CREATE POLICY dispatch_prune ON group_ledger FOR ALL TO taskiem_dispatch USING (true);
GRANT SELECT (person_id, at), DELETE ON group_ledger TO taskiem_dispatch;

-- The safety job's sweep: as 00481's, and the ledger past two days.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_sweep(p_live interval)
RETURNS TABLE (what text, n bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a bigint;
  b bigint;
  c bigint;
  d bigint;
BEGIN
  PERFORM taskiem_moderator_check();
  DELETE FROM people_gone WHERE at < now() - interval '1 hour';
  GET DIAGNOSTICS a = ROW_COUNT;
  DELETE FROM people_typing WHERE until < now();
  GET DIAGNOSTICS b = ROW_COUNT;
  DELETE FROM person_live WHERE seen_at < now() - p_live;
  GET DIAGNOSTICS c = ROW_COUNT;
  DELETE FROM group_ledger WHERE at < now() - interval '2 days';
  GET DIAGNOSTICS d = ROW_COUNT;
  RETURN QUERY VALUES ('people_gone', a), ('people_typing', b), ('person_live', c), ('group_ledger', d);
END
$$;
-- +goose StatementEnd

-- Deleting an account: as 00483's, and their ledger.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_group_forget() RETURNS void
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
  DELETE FROM group_ledger l WHERE l.person_id = v_me;
END
$$;
-- +goose StatementEnd

-- --- reports of a group or a member ---

ALTER TABLE people_reports
  ADD COLUMN group_id     uuid,
  ADD COLUMN group_sealed bytea,
  ADD CONSTRAINT people_reports_group CHECK (group_sealed IS NULL OR group_id IS NOT NULL);
CREATE INDEX people_reports_group ON people_reports (group_id, created_at) WHERE group_id IS NOT NULL;
-- A report of a group is made by someone who is or was in it.
DROP POLICY person_writes ON people_reports;
CREATE POLICY person_writes ON people_reports FOR INSERT TO taskiem_app
  WITH CHECK (person_id = taskiem_person_scope() AND (group_id IS NULL OR taskiem_group_member(group_id)));

-- --- held group messages and the checks' verdicts ---

ALTER TABLE people_held
  ALTER COLUMN chat_id DROP NOT NULL,
  ADD COLUMN group_id uuid,
  ADD COLUMN mentions uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(mentions) <= 256),
  ADD CONSTRAINT people_held_where CHECK ((chat_id IS NULL) <> (group_id IS NULL));
CREATE INDEX people_held_group ON people_held (group_id) WHERE group_id IS NOT NULL;
DROP POLICY person_writes ON people_held;
CREATE POLICY person_writes ON people_held FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND sender_id = taskiem_person_scope()
    AND (group_id IS NULL OR EXISTS (SELECT 1 FROM group_members m WHERE m.group_id = people_held.group_id AND m.person_id = taskiem_person_scope()
                                       AND m.left_at IS NULL)));

ALTER TABLE people_screen_verdicts ADD COLUMN group_id uuid;
GRANT SELECT (group_id) ON people_screen_verdicts TO taskiem_safety;

-- A group message the checks warned of (B1): shown with the warning to
-- the members who have not added its sender.
ALTER TABLE group_messages ADD COLUMN screen text CHECK (screen IN ('warn', 'transfer'));

-- --- moderators' actions on a group ---

ALTER TABLE people_moderation
  ALTER COLUMN person_id DROP NOT NULL,
  ADD COLUMN group_id uuid,
  DROP CONSTRAINT people_moderation_action_check,
  ADD CONSTRAINT people_moderation_action_check CHECK (action IN ('warn', 'limit', 'suspend', 'close', 'group_link_off', 'group_close')),
  DROP CONSTRAINT people_moderation_check,
  ADD CONSTRAINT people_moderation_check CHECK (action NOT IN ('limit', 'close', 'group_close') OR until IS NOT NULL),
  ADD CONSTRAINT people_moderation_whom CHECK (CASE WHEN action IN ('group_link_off', 'group_close') THEN person_id IS NULL AND group_id IS NOT NULL
                                                    ELSE person_id IS NOT NULL AND group_id IS NULL END);
CREATE INDEX people_moderation_group ON people_moderation (group_id, created_at) WHERE group_id IS NOT NULL;
-- A group's admins read the actions on it (to appeal a closure).
CREATE POLICY group_admin_read ON people_moderation FOR SELECT TO taskiem_app
  USING (group_id IS NOT NULL AND taskiem_person_business() IS NULL AND taskiem_group_admin(group_id));

-- An appeal of an action on a group is made by one of its admins, once;
-- the group's admins read it.
DROP POLICY person_own ON people_appeals;
CREATE POLICY person_own ON people_appeals TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope() AND state = 'open'
    AND EXISTS (SELECT 1 FROM people_moderation m WHERE m.id = action_id
                  AND (m.person_id = taskiem_person_scope() OR (m.group_id IS NOT NULL AND taskiem_group_admin(m.group_id)))));
CREATE POLICY group_admin_read ON people_appeals FOR SELECT TO taskiem_app
  USING (taskiem_person_business() IS NULL
    AND EXISTS (SELECT 1 FROM people_moderation m WHERE m.id = action_id AND m.group_id IS NOT NULL AND taskiem_group_admin(m.group_id)));

-- What is in force on a group: closed (no one writes in it; deleted at
-- close_until unless an appeal holds it) and its link stopped (admins make
-- no new one). Read by its members and anyone invited; written only by
-- taskiem_safety's definers.
CREATE TABLE group_standing (
  group_id         uuid PRIMARY KEY REFERENCES people_groups(id) ON DELETE CASCADE,
  closed_at        timestamptz,
  close_until      timestamptz,
  close_action     uuid,
  link_stopped_at  timestamptz,
  changed_at       timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE group_standing ENABLE ROW LEVEL SECURITY;
ALTER TABLE group_standing FORCE ROW LEVEL SECURITY;
REVOKE ALL ON group_standing FROM PUBLIC;
CREATE POLICY member_read ON group_standing FOR SELECT TO taskiem_app
  USING (taskiem_group_member(group_id) OR taskiem_group_invited(group_id));
GRANT SELECT ON group_standing TO taskiem_app;
CREATE POLICY safety_all ON group_standing TO taskiem_safety USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON group_standing TO taskiem_safety;

-- What the safety definers reach of a group: its times and maker, its
-- links (turned off), Taskiem's event messages, and deleting it.
GRANT SELECT (created_at, created_by, last_message_at), DELETE ON people_groups TO taskiem_safety;
CREATE POLICY safety_link_off ON group_links FOR UPDATE TO taskiem_safety USING (true) WITH CHECK (true);
GRANT UPDATE (replaced_at) ON group_links TO taskiem_safety;
CREATE POLICY safety_event ON group_messages FOR INSERT TO taskiem_safety WITH CHECK (kind = 'event' AND sender_id IS NULL);
GRANT INSERT ON group_messages TO taskiem_safety;

-- --- lawful requests ---

ALTER TABLE lawful_requests ADD COLUMN scope_groups text NOT NULL DEFAULT 'none' CHECK (scope_groups IN ('none', 'sent', 'seen'));

-- --- the definers ---

-- What is in force on a group, from the actions (a closure turns its link
-- off too), with the members told by an event from Taskiem when it
-- changes. Called by the moderators' definers, as taskiem_safety.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_standing_sync(p_group uuid) RETURNS void
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  v_close people_moderation%ROWTYPE;
  v_closed boolean;
  v_link boolean;
  v_was_closed boolean := false;
  v_was_link boolean := false;
  v_type text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM people_groups g WHERE g.id = p_group) THEN
    RETURN;
  END IF;
  SELECT * INTO v_close FROM people_moderation m WHERE m.group_id = p_group AND m.action = 'group_close' AND m.lifted_at IS NULL AND m.closed_at IS NULL
   ORDER BY m.created_at DESC LIMIT 1;
  v_closed := FOUND;
  v_link := v_closed OR EXISTS (SELECT 1 FROM people_moderation m WHERE m.group_id = p_group AND m.action = 'group_link_off' AND m.lifted_at IS NULL);
  SELECT s.closed_at IS NOT NULL, s.link_stopped_at IS NOT NULL INTO v_was_closed, v_was_link FROM group_standing s WHERE s.group_id = p_group;
  v_was_closed := COALESCE(v_was_closed, false);
  v_was_link := COALESCE(v_was_link, false);
  INSERT INTO group_standing (group_id, closed_at, close_until, close_action, link_stopped_at)
  VALUES (p_group, CASE WHEN v_closed THEN v_close.created_at END, CASE WHEN v_closed THEN v_close.until END, CASE WHEN v_closed THEN v_close.id END,
          CASE WHEN v_link THEN now() END)
  ON CONFLICT (group_id) DO UPDATE SET closed_at = EXCLUDED.closed_at, close_until = EXCLUDED.close_until, close_action = EXCLUDED.close_action,
    link_stopped_at = CASE WHEN v_link THEN COALESCE(group_standing.link_stopped_at, now()) END, changed_at = now();
  IF v_link THEN
    UPDATE group_links SET replaced_at = now() WHERE group_id = p_group AND replaced_at IS NULL;
  END IF;
  v_type := CASE
    WHEN v_closed AND NOT v_was_closed THEN 'taskiem_closed'
    WHEN v_was_closed AND NOT v_closed THEN 'taskiem_reopened'
    WHEN NOT v_closed AND v_link AND NOT v_was_link THEN 'taskiem_link_off'
    WHEN NOT v_closed AND v_was_link AND NOT v_link THEN 'taskiem_link_back'
  END;
  IF v_type IS NOT NULL THEN
    INSERT INTO group_messages (group_id, id, sender_id, kind, event)
    VALUES (p_group, gen_random_uuid(), NULL, 'event',
            jsonb_build_object('type', v_type, 'actor', '00000000-0000-0000-0000-000000000000'::uuid, 'until', CASE WHEN v_closed THEN v_close.until END));
    UPDATE people_groups SET last_message_at = clock_timestamp() WHERE id = p_group;
  END IF;
END
$$;
-- +goose StatementEnd

-- Turns a group's link off, or closes it (deleted at p_until unless an
-- appeal holds it), with the reason and the operator. The group's row is
-- taken first, as every writer of a group does.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_group_moderate(p_group uuid, p_action text, p_reason text, p_by text, p_report uuid, p_until timestamptz) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id uuid := gen_random_uuid();
BEGIN
  PERFORM taskiem_moderator_check();
  IF COALESCE(p_by, '') = '' OR COALESCE(btrim(p_reason), '') = '' OR p_action NOT IN ('group_link_off', 'group_close') THEN
    RAISE EXCEPTION 'a moderator, a reason and group_link_off or group_close are needed' USING ERRCODE = '22023';
  END IF;
  PERFORM 1 FROM people_groups g WHERE g.id = p_group FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such group' USING ERRCODE = 'P0002';
  END IF;
  IF EXISTS (SELECT 1 FROM people_moderation m WHERE m.group_id = p_group AND m.lifted_at IS NULL AND m.closed_at IS NULL
               AND (m.action = 'group_close' OR m.action = p_action)) THEN
    RAISE EXCEPTION 'this is already in force on the group' USING ERRCODE = '55000';
  END IF;
  INSERT INTO people_moderation (id, group_id, action, reason, operator, report_id, until)
  VALUES (v_id, p_group, p_action, btrim(p_reason), p_by, p_report, CASE WHEN p_action = 'group_close' THEN p_until END);
  PERFORM taskiem_group_standing_sync(p_group);
  RETURN v_id;
END
$$;
-- +goose StatementEnd

-- Lifting an action: a person's, or a group's (the group's standing
-- follows). Answers whose.
DROP FUNCTION taskiem_ops_people_lift(uuid, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_lift(p_action uuid, p_by text, p_reason text)
RETURNS TABLE (person_id uuid, group_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_person uuid;
  v_group uuid;
  v_found boolean;
BEGIN
  PERFORM taskiem_moderator_check();
  -- The closure's lock first (taskiem_people_closure_due).
  PERFORM 1 FROM people_moderation m WHERE m.id = p_action FOR UPDATE;
  UPDATE people_moderation m SET lifted_at = now(), lifted_by = p_by, lift_reason = NULLIF(btrim(p_reason), '')
   WHERE m.id = p_action AND m.lifted_at IS NULL AND m.closed_at IS NULL AND m.action <> 'warn'
  RETURNING m.person_id, m.group_id INTO v_person, v_group;
  v_found := FOUND;
  IF NOT v_found THEN
    IF EXISTS (SELECT 1 FROM people_moderation m WHERE m.id = p_action) THEN
      RAISE EXCEPTION 'this action is not in force' USING ERRCODE = '55000';
    END IF;
    RAISE EXCEPTION 'no such action' USING ERRCODE = 'P0002';
  END IF;
  IF v_group IS NOT NULL THEN
    PERFORM taskiem_group_standing_sync(v_group);
  ELSE
    PERFORM taskiem_people_standing_sync(v_person);
  END IF;
  RETURN QUERY SELECT v_person, v_group;
END
$$;
-- +goose StatementEnd

-- Deciding an appeal: as 00481's, for a group's action too (overturning
-- reopens the group). Answers who appealed, and the group.
DROP FUNCTION taskiem_ops_people_appeal_decide(uuid, text, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_appeal_decide(p_id uuid, p_state text, p_by text, p_note text)
RETURNS TABLE (person_id uuid, group_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_person uuid;
  v_action uuid;
  v_group uuid;
  v_closed timestamptz;
BEGIN
  PERFORM taskiem_moderator_check();
  IF p_state NOT IN ('upheld', 'overturned') OR COALESCE(p_by, '') = '' THEN
    RAISE EXCEPTION 'upheld or overturned, by a moderator' USING ERRCODE = '22023';
  END IF;
  -- The action's lock first (taskiem_people_closure_due,
  -- taskiem_group_close_carry).
  SELECT m.closed_at, m.group_id INTO v_closed, v_group FROM people_moderation m
   WHERE m.id = (SELECT a.action_id FROM people_appeals a WHERE a.id = p_id) FOR UPDATE;
  IF EXISTS (SELECT 1 FROM people_appeals a JOIN people_moderation m ON m.id = a.action_id WHERE a.id = p_id AND m.operator = p_by) THEN
    RAISE EXCEPTION 'a different moderator answers an appeal: not the one who decided' USING ERRCODE = '42501';
  END IF;
  IF v_closed IS NOT NULL AND EXISTS (SELECT 1 FROM people_appeals a WHERE a.id = p_id AND a.state = 'open') THEN
    RAISE EXCEPTION 'this was already carried out' USING ERRCODE = '55000';
  END IF;
  UPDATE people_appeals a SET state = p_state, decided_by = p_by, decided_at = now(), decision_note = NULLIF(btrim(p_note), '')
   WHERE a.id = p_id AND a.state = 'open' RETURNING a.person_id, a.action_id INTO v_person, v_action;
  IF v_action IS NULL THEN
    IF EXISTS (SELECT 1 FROM people_appeals a WHERE a.id = p_id) THEN
      RAISE EXCEPTION 'this appeal was already decided' USING ERRCODE = '55000';
    END IF;
    RAISE EXCEPTION 'no such appeal' USING ERRCODE = 'P0002';
  END IF;
  IF p_state = 'overturned' THEN
    UPDATE people_moderation m SET lifted_at = now(), lifted_by = p_by, lift_reason = COALESCE(NULLIF(btrim(p_note), ''), 'Appeal overturned')
     WHERE m.id = v_action AND m.lifted_at IS NULL AND m.closed_at IS NULL;
    IF v_group IS NOT NULL THEN
      PERFORM taskiem_group_standing_sync(v_group);
    ELSE
      PERFORM taskiem_people_standing_sync(v_person);
    END IF;
  END IF;
  RETURN QUERY SELECT v_person, v_group;
END
$$;
-- +goose StatementEnd

-- The safety job: group closures whose day came with no appeal open.
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_closures_due()
RETURNS TABLE (action_id uuid, group_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT m.id, m.group_id FROM people_moderation m
   WHERE m.action = 'group_close' AND m.lifted_at IS NULL AND m.closed_at IS NULL AND m.until <= now()
     AND NOT EXISTS (SELECT 1 FROM people_appeals a WHERE a.action_id = m.id AND a.state = 'open')
   ORDER BY m.until LIMIT 50;
END
$$;
-- +goose StatementEnd

-- Carries out one group closure, in one transaction: the action locked
-- and checked still due (not lifted or carried out, its day come, no
-- appeal open, read after the lock), then the group and all of it deleted
-- (§14; reports keep their own copies). Answers what became of it:
-- not_due, deleted, or gone (the group was already deleted).
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_close_carry(p_action uuid) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  m people_moderation%ROWTYPE;
  n bigint;
  v_note text;
BEGIN
  PERFORM taskiem_moderator_check();
  SELECT * INTO m FROM people_moderation x WHERE x.id = p_action FOR UPDATE;
  IF NOT FOUND OR m.action <> 'group_close' OR m.lifted_at IS NOT NULL OR m.closed_at IS NOT NULL OR m.until > now() THEN
    RETURN 'not_due';
  END IF;
  IF EXISTS (SELECT 1 FROM people_appeals a WHERE a.action_id = p_action AND a.state = 'open') THEN
    RETURN 'not_due';
  END IF;
  DELETE FROM people_groups g WHERE g.id = m.group_id;
  GET DIAGNOSTICS n = ROW_COUNT;
  v_note := CASE WHEN n > 0 THEN 'deleted' ELSE 'gone' END;
  UPDATE people_moderation x SET closed_at = now(), close_note = v_note WHERE x.id = p_action;
  RETURN v_note;
END
$$;
-- +goose StatementEnd

-- A group, for moderators: its times, maker, sizes and standing; never
-- its name or messages.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_group(p_group uuid)
RETURNS TABLE (id uuid, created_at timestamptz, created_by uuid, creator_name text, members int, admins int, link_on boolean, last_message_at timestamptz,
  closed_at timestamptz, close_until timestamptz, link_stopped_at timestamptz, reports bigint, held bigint)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT g.id, g.created_at, g.created_by, COALESCE(p.name, ''),
         (SELECT count(*)::int FROM group_members m WHERE m.group_id = g.id AND m.left_at IS NULL),
         (SELECT count(*)::int FROM group_members m WHERE m.group_id = g.id AND m.left_at IS NULL AND m.role = 'admin'),
         EXISTS (SELECT 1 FROM group_links l WHERE l.group_id = g.id AND l.replaced_at IS NULL),
         g.last_message_at, s.closed_at, s.close_until, s.link_stopped_at,
         (SELECT count(*) FROM people_reports r WHERE r.group_id = g.id),
         (SELECT count(*) FROM people_held h WHERE h.group_id = g.id)
    FROM people_groups g LEFT JOIN person_profiles p ON p.person_id = g.created_by LEFT JOIN group_standing s ON s.group_id = g.id
   WHERE g.id = p_group;
END
$$;
-- +goose StatementEnd

-- A group's members and those who left, for moderators to act on: names
-- and roles, never their messages.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_group_members(p_group uuid)
RETURNS TABLE (person_id uuid, name text, role text, how text, joined_at timestamptz, left_at timestamptz, left_how text, deleted boolean,
  suspended boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT m.person_id, COALESCE(p.name, ''), m.role, m.how, m.joined_at, m.left_at, m.left_how, COALESCE(p.deleted, true), COALESCE(p.suspended, false)
    FROM group_members m LEFT JOIN person_profiles p ON p.person_id = m.person_id
   WHERE m.group_id = p_group
   ORDER BY m.left_at IS NOT NULL, m.role, m.joined_at LIMIT 300;
END
$$;
-- +goose StatementEnd

-- The actions on a group, with their appeals (kept after the group goes).
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_group_actions(p_group uuid)
RETURNS TABLE (id uuid, action text, reason text, operator text, report_id uuid, until timestamptz, lifted_at timestamptz, lifted_by text,
  lift_reason text, closed_at timestamptz, close_note text, created_at timestamptz, appeal_id uuid, appeal_state text, appeal_text text,
  appeal_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT m.id, m.action, m.reason, m.operator, m.report_id, m.until, m.lifted_at, m.lifted_by, m.lift_reason, m.closed_at, m.close_note, m.created_at,
         a.id, a.state, a.text, a.created_at
    FROM people_moderation m LEFT JOIN people_appeals a ON a.action_id = m.id
   WHERE m.group_id = p_group ORDER BY m.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

-- The reports of a group or its members, without their words.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_group_reports(p_group uuid)
RETURNS TABLE (id uuid, reporter_id uuid, reported_id uuid, reported_name text, reason text, status text, created_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.reported_id, COALESCE(p.name, ''), r.reason, r.status, r.created_at
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id
   WHERE r.group_id = p_group ORDER BY r.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

-- The reports' list: as 00481's, with the group.
DROP FUNCTION taskiem_ops_people_reports(text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_reports(p_status text)
RETURNS TABLE (id uuid, reporter_id uuid, detached boolean, reported_id uuid, reported_name text, reported_gone boolean, reported_suspended boolean,
  reason text, note text, from_request boolean, files int, sealed boolean, status text, created_at timestamptz, reviewed_by text, reviewed_at timestamptz,
  reports_against bigint, group_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.detached_at IS NOT NULL, r.reported_id, COALESCE(p.name, ''), COALESCE(p.deleted, false), COALESCE(p.suspended, false),
         r.reason, r.note, r.from_request, r.files, r.sealed IS NOT NULL, r.status, r.created_at, r.reviewed_by, r.reviewed_at,
         CASE WHEN r.reported_id IS NULL THEN (SELECT count(DISTINCT taskiem_people_reporter(q.reporter_mark, q.person_id, q.id)) FROM people_reports q
                                                WHERE q.group_id = r.group_id AND q.reported_id IS NULL)
              ELSE (SELECT count(DISTINCT taskiem_people_reporter(q.reporter_mark, q.person_id, q.id)) FROM people_reports q WHERE q.reported_id = r.reported_id) END,
         r.group_id
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id
   WHERE r.status = p_status OR p_status = 'all'
   ORDER BY r.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

-- One report: as 00480's, with the group and its sealed info.
DROP FUNCTION taskiem_ops_people_report(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_report(p_id uuid)
RETURNS TABLE (id uuid, reporter_id uuid, reported_id uuid, reported_name text, reason text, note text, from_request boolean, messages jsonb, sealed bytea,
  box_key text, kms_key text, status text, created_at timestamptz, reviewed_by text, reviewed_at timestamptz, review_note text, group_id uuid,
  group_sealed bytea)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.reported_id, COALESCE(p.name, ''), r.reason, r.note, r.from_request, r.messages, r.sealed, r.box_key, r.kms_key,
         r.status, r.created_at, r.reviewed_by, r.reviewed_at, r.review_note, r.group_id, r.group_sealed
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id WHERE r.id = p_id;
END
$$;
-- +goose StatementEnd

-- Held messages: as 00480's, with the group.
DROP FUNCTION taskiem_ops_people_held(text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_held(p_state text)
RETURNS TABLE (id uuid, sender_id uuid, sender_name text, recipient_id uuid, recipient_name text, kind text, reasons text[], has_file boolean, state text,
  created_at timestamptz, decided_by text, decided_at timestamptz, group_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT h.id, h.sender_id, COALESCE(s.name, ''), h.recipient_id, COALESCE(r.name, ''), h.kind, h.reasons, h.file_sealed IS NOT NULL, h.state,
         h.created_at, h.decided_by, h.decided_at, h.group_id
    FROM people_held h LEFT JOIN person_profiles s ON s.person_id = h.sender_id LEFT JOIN person_profiles r ON r.person_id = h.recipient_id
   WHERE h.state = p_state OR p_state = 'all'
   ORDER BY h.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_held_one(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_held_one(p_id uuid)
RETURNS TABLE (id uuid, sender_id uuid, sender_name text, recipient_id uuid, recipient_name text, chat_id uuid, kind text, reply_to uuid, forwards int,
  reasons text[], sealed bytea, file_sealed bytea, file_type text, file_size bigint, box_key text, kms_key text, state text, created_at timestamptz,
  decided_by text, decided_at timestamptz, decision_note text, released_message uuid, group_id uuid, mentions uuid[])
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT h.id, h.sender_id, COALESCE(s.name, ''), h.recipient_id, COALESCE(r.name, ''), h.chat_id, h.kind, h.reply_to, h.forwards, h.reasons, h.sealed,
         h.file_sealed, h.file_type, h.file_size, h.box_key, h.kms_key, h.state, h.created_at, h.decided_by, h.decided_at, h.decision_note,
         h.released_message, h.group_id, h.mentions
    FROM people_held h LEFT JOIN person_profiles s ON s.person_id = h.sender_id LEFT JOIN person_profiles r ON r.person_id = h.recipient_id
   WHERE h.id = p_id;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_verdicts(int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_verdicts(p_limit int)
RETURNS TABLE (from_person uuid, from_name text, to_person uuid, to_name text, verdict text, reasons text[], at timestamptz, group_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT v.from_person, COALESCE(f.name, ''), v.to_person, COALESCE(t.name, ''), v.verdict, v.reasons, v.at, v.group_id
    FROM people_screen_verdicts v LEFT JOIN person_profiles f ON f.person_id = v.from_person LEFT JOIN person_profiles t ON t.person_id = v.to_person
   ORDER BY v.at DESC LIMIT LEAST(GREATEST(p_limit, 1), 500);
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_appeals(text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_appeals(p_state text)
RETURNS TABLE (id uuid, action_id uuid, person_id uuid, name text, action text, action_reason text, operator text, until timestamptz, text text,
  state text, created_at timestamptz, decided_by text, decided_at timestamptz, decision_note text, group_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT a.id, a.action_id, a.person_id, COALESCE(p.name, ''), m.action, m.reason, m.operator, m.until, a.text, a.state, a.created_at, a.decided_by,
         a.decided_at, a.decision_note, m.group_id
    FROM people_appeals a JOIN people_moderation m ON m.id = a.action_id LEFT JOIN person_profiles p ON p.person_id = a.person_id
   WHERE a.state = p_state OR p_state = 'all'
   ORDER BY a.created_at LIMIT 200;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_lawful_record(uuid, text, text, text, uuid, timestamptz, timestamptz, uuid, boolean, text, timestamptz, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_lawful_record(p_id uuid, p_authority text, p_reference text, p_basis text, p_person uuid, p_from timestamptz,
  p_to timestamptz, p_with uuid, p_files boolean, p_note text, p_received timestamptz, p_by text, p_groups text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = p_person) THEN
    RAISE EXCEPTION 'no such account' USING ERRCODE = 'P0002';
  END IF;
  INSERT INTO lawful_requests (id, authority, reference, legal_basis, person_id, scope_from, scope_to, scope_with, scope_files, scope_note, received_at,
    recorded_by, scope_groups)
  VALUES (p_id, btrim(p_authority), btrim(p_reference), btrim(p_basis), p_person, p_from, p_to, p_with, p_files, COALESCE(btrim(p_note), ''), p_received,
    p_by, COALESCE(p_groups, 'none'));
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_lawful_requests(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_lawful_requests(p_id uuid)
RETURNS TABLE (id uuid, authority text, reference text, legal_basis text, person_id uuid, person_name text, scope_from timestamptz, scope_to timestamptz,
  scope_with uuid, scope_files boolean, scope_note text, received_at timestamptz, recorded_by text, recorded_at timestamptz, approved_by text,
  approved_at timestamptz, state text, answered_by text, answered_at timestamptz, messages_given int, refused_reason text, told_at timestamptz,
  not_told_reason text, scope_groups text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT l.id, l.authority, l.reference, l.legal_basis, l.person_id, COALESCE(p.name, ''), l.scope_from, l.scope_to, l.scope_with, l.scope_files,
         l.scope_note, l.received_at, l.recorded_by, l.recorded_at, l.approved_by, l.approved_at, l.state, l.answered_by, l.answered_at,
         l.messages_given, l.refused_reason, l.told_at, l.not_told_reason, l.scope_groups
    FROM lawful_requests l LEFT JOIN person_profiles p ON p.person_id = l.person_id
   WHERE p_id IS NULL OR l.id = p_id
   ORDER BY l.received_at DESC LIMIT 500;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_ops_group_moderate(uuid, text, text, text, uuid, timestamptz)',
                           'taskiem_ops_people_lift(uuid, text, text)',
                           'taskiem_ops_people_appeal_decide(uuid, text, text, text)',
                           'taskiem_group_closures_due()',
                           'taskiem_group_close_carry(uuid)',
                           'taskiem_ops_group(uuid)',
                           'taskiem_ops_group_members(uuid)',
                           'taskiem_ops_group_actions(uuid)',
                           'taskiem_ops_group_reports(uuid)',
                           'taskiem_ops_people_reports(text)',
                           'taskiem_ops_people_report(uuid)',
                           'taskiem_ops_people_held(text)',
                           'taskiem_ops_people_held_one(uuid)',
                           'taskiem_ops_people_verdicts(int)',
                           'taskiem_ops_people_appeals(text)',
                           'taskiem_ops_lawful_record(uuid, text, text, text, uuid, timestamptz, timestamptz, uuid, boolean, text, timestamptz, text, text)',
                           'taskiem_ops_lawful_requests(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_safety', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
  -- Not the app's: the definers above call it.
  EXECUTE 'ALTER FUNCTION taskiem_group_standing_sync(uuid) OWNER TO taskiem_safety';
  EXECUTE 'REVOKE EXECUTE ON FUNCTION taskiem_group_standing_sync(uuid) FROM PUBLIC';
END $$;
-- +goose StatementEnd

-- +goose Down
-- The functions as 00480 and 00481 left them.
DROP FUNCTION taskiem_ops_lawful_requests(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_lawful_requests(p_id uuid)
RETURNS TABLE (id uuid, authority text, reference text, legal_basis text, person_id uuid, person_name text, scope_from timestamptz, scope_to timestamptz,
  scope_with uuid, scope_files boolean, scope_note text, received_at timestamptz, recorded_by text, recorded_at timestamptz, approved_by text,
  approved_at timestamptz, state text, answered_by text, answered_at timestamptz, messages_given int, refused_reason text, told_at timestamptz,
  not_told_reason text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT l.id, l.authority, l.reference, l.legal_basis, l.person_id, COALESCE(p.name, ''), l.scope_from, l.scope_to, l.scope_with, l.scope_files,
         l.scope_note, l.received_at, l.recorded_by, l.recorded_at, l.approved_by, l.approved_at, l.state, l.answered_by, l.answered_at,
         l.messages_given, l.refused_reason, l.told_at, l.not_told_reason
    FROM lawful_requests l LEFT JOIN person_profiles p ON p.person_id = l.person_id
   WHERE p_id IS NULL OR l.id = p_id
   ORDER BY l.received_at DESC LIMIT 500;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_lawful_record(uuid, text, text, text, uuid, timestamptz, timestamptz, uuid, boolean, text, timestamptz, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_lawful_record(p_id uuid, p_authority text, p_reference text, p_basis text, p_person uuid, p_from timestamptz,
  p_to timestamptz, p_with uuid, p_files boolean, p_note text, p_received timestamptz, p_by text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = p_person) THEN
    RAISE EXCEPTION 'no such account' USING ERRCODE = 'P0002';
  END IF;
  INSERT INTO lawful_requests (id, authority, reference, legal_basis, person_id, scope_from, scope_to, scope_with, scope_files, scope_note, received_at,
    recorded_by)
  VALUES (p_id, btrim(p_authority), btrim(p_reference), btrim(p_basis), p_person, p_from, p_to, p_with, p_files, COALESCE(btrim(p_note), ''), p_received,
    p_by);
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_appeals(text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_appeals(p_state text)
RETURNS TABLE (id uuid, action_id uuid, person_id uuid, name text, action text, action_reason text, operator text, until timestamptz, text text,
  state text, created_at timestamptz, decided_by text, decided_at timestamptz, decision_note text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT a.id, a.action_id, a.person_id, COALESCE(p.name, ''), m.action, m.reason, m.operator, m.until, a.text, a.state, a.created_at, a.decided_by,
         a.decided_at, a.decision_note
    FROM people_appeals a JOIN people_moderation m ON m.id = a.action_id LEFT JOIN person_profiles p ON p.person_id = a.person_id
   WHERE a.state = p_state OR p_state = 'all'
   ORDER BY a.created_at LIMIT 200;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_verdicts(int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_verdicts(p_limit int)
RETURNS TABLE (from_person uuid, from_name text, to_person uuid, to_name text, verdict text, reasons text[], at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT v.from_person, COALESCE(f.name, ''), v.to_person, COALESCE(t.name, ''), v.verdict, v.reasons, v.at
    FROM people_screen_verdicts v LEFT JOIN person_profiles f ON f.person_id = v.from_person LEFT JOIN person_profiles t ON t.person_id = v.to_person
   ORDER BY v.at DESC LIMIT LEAST(GREATEST(p_limit, 1), 500);
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_held_one(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_held_one(p_id uuid)
RETURNS TABLE (id uuid, sender_id uuid, sender_name text, recipient_id uuid, recipient_name text, chat_id uuid, kind text, reply_to uuid, forwards int,
  reasons text[], sealed bytea, file_sealed bytea, file_type text, file_size bigint, box_key text, kms_key text, state text, created_at timestamptz,
  decided_by text, decided_at timestamptz, decision_note text, released_message uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT h.id, h.sender_id, COALESCE(s.name, ''), h.recipient_id, COALESCE(r.name, ''), h.chat_id, h.kind, h.reply_to, h.forwards, h.reasons, h.sealed,
         h.file_sealed, h.file_type, h.file_size, h.box_key, h.kms_key, h.state, h.created_at, h.decided_by, h.decided_at, h.decision_note,
         h.released_message
    FROM people_held h LEFT JOIN person_profiles s ON s.person_id = h.sender_id LEFT JOIN person_profiles r ON r.person_id = h.recipient_id
   WHERE h.id = p_id;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_held(text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_held(p_state text)
RETURNS TABLE (id uuid, sender_id uuid, sender_name text, recipient_id uuid, recipient_name text, kind text, reasons text[], has_file boolean, state text,
  created_at timestamptz, decided_by text, decided_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT h.id, h.sender_id, COALESCE(s.name, ''), h.recipient_id, COALESCE(r.name, ''), h.kind, h.reasons, h.file_sealed IS NOT NULL, h.state,
         h.created_at, h.decided_by, h.decided_at
    FROM people_held h LEFT JOIN person_profiles s ON s.person_id = h.sender_id LEFT JOIN person_profiles r ON r.person_id = h.recipient_id
   WHERE h.state = p_state OR p_state = 'all'
   ORDER BY h.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_report(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_report(p_id uuid)
RETURNS TABLE (id uuid, reporter_id uuid, reported_id uuid, reported_name text, reason text, note text, from_request boolean, messages jsonb, sealed bytea,
  box_key text, kms_key text, status text, created_at timestamptz, reviewed_by text, reviewed_at timestamptz, review_note text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.reported_id, COALESCE(p.name, ''), r.reason, r.note, r.from_request, r.messages, r.sealed, r.box_key, r.kms_key,
         r.status, r.created_at, r.reviewed_by, r.reviewed_at, r.review_note
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id WHERE r.id = p_id;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_reports(text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_reports(p_status text)
RETURNS TABLE (id uuid, reporter_id uuid, detached boolean, reported_id uuid, reported_name text, reported_gone boolean, reported_suspended boolean,
  reason text, note text, from_request boolean, files int, sealed boolean, status text, created_at timestamptz, reviewed_by text, reviewed_at timestamptz,
  reports_against bigint)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.detached_at IS NOT NULL, r.reported_id, COALESCE(p.name, ''), COALESCE(p.deleted, false), COALESCE(p.suspended, false),
         r.reason, r.note, r.from_request, r.files, r.sealed IS NOT NULL, r.status, r.created_at, r.reviewed_by, r.reviewed_at,
         (SELECT count(DISTINCT taskiem_people_reporter(q.reporter_mark, q.person_id, q.id)) FROM people_reports q WHERE q.reported_id = r.reported_id)
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id
   WHERE r.status = p_status OR p_status = 'all'
   ORDER BY r.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_group_reports(uuid);
DROP FUNCTION taskiem_ops_group_actions(uuid);
DROP FUNCTION taskiem_ops_group_members(uuid);
DROP FUNCTION taskiem_ops_group(uuid);
DROP FUNCTION taskiem_group_close_carry(uuid);
DROP FUNCTION taskiem_group_closures_due();

DROP FUNCTION taskiem_ops_people_appeal_decide(uuid, text, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_appeal_decide(p_id uuid, p_state text, p_by text, p_note text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_person uuid;
  v_action uuid;
  v_closed timestamptz;
BEGIN
  PERFORM taskiem_moderator_check();
  IF p_state NOT IN ('upheld', 'overturned') OR COALESCE(p_by, '') = '' THEN
    RAISE EXCEPTION 'upheld or overturned, by a moderator' USING ERRCODE = '22023';
  END IF;
  SELECT m.closed_at INTO v_closed FROM people_moderation m WHERE m.id = (SELECT a.action_id FROM people_appeals a WHERE a.id = p_id) FOR UPDATE;
  IF EXISTS (SELECT 1 FROM people_appeals a JOIN people_moderation m ON m.id = a.action_id WHERE a.id = p_id AND m.operator = p_by) THEN
    RAISE EXCEPTION 'a different moderator answers an appeal: not the one who decided' USING ERRCODE = '42501';
  END IF;
  IF v_closed IS NOT NULL AND EXISTS (SELECT 1 FROM people_appeals a WHERE a.id = p_id AND a.state = 'open') THEN
    RAISE EXCEPTION 'this account was already closed' USING ERRCODE = '55000';
  END IF;
  UPDATE people_appeals SET state = p_state, decided_by = p_by, decided_at = now(), decision_note = NULLIF(btrim(p_note), '')
   WHERE id = p_id AND state = 'open' RETURNING person_id, action_id INTO v_person, v_action;
  IF v_person IS NULL THEN
    IF EXISTS (SELECT 1 FROM people_appeals WHERE id = p_id) THEN
      RAISE EXCEPTION 'this appeal was already decided' USING ERRCODE = '55000';
    END IF;
    RAISE EXCEPTION 'no such appeal' USING ERRCODE = 'P0002';
  END IF;
  IF p_state = 'overturned' THEN
    UPDATE people_moderation SET lifted_at = now(), lifted_by = p_by, lift_reason = COALESCE(NULLIF(btrim(p_note), ''), 'Appeal overturned')
     WHERE id = v_action AND lifted_at IS NULL AND closed_at IS NULL;
    PERFORM taskiem_people_standing_sync(v_person);
  END IF;
  RETURN v_person;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_people_lift(uuid, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_lift(p_action uuid, p_by text, p_reason text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_person uuid;
BEGIN
  PERFORM taskiem_moderator_check();
  PERFORM 1 FROM people_moderation WHERE id = p_action FOR UPDATE;
  UPDATE people_moderation SET lifted_at = now(), lifted_by = p_by, lift_reason = NULLIF(btrim(p_reason), '')
   WHERE id = p_action AND lifted_at IS NULL AND closed_at IS NULL AND action <> 'warn'
  RETURNING person_id INTO v_person;
  IF v_person IS NULL THEN
    IF EXISTS (SELECT 1 FROM people_moderation WHERE id = p_action) THEN
      RAISE EXCEPTION 'this action is not in force' USING ERRCODE = '55000';
    END IF;
    RAISE EXCEPTION 'no such action' USING ERRCODE = 'P0002';
  END IF;
  PERFORM taskiem_people_standing_sync(v_person);
  RETURN v_person;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_group_moderate(uuid, text, text, text, uuid, timestamptz);
DROP FUNCTION taskiem_group_standing_sync(uuid);

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_ops_people_lift(uuid, text, text)',
                           'taskiem_ops_people_appeal_decide(uuid, text, text, text)',
                           'taskiem_ops_people_reports(text)',
                           'taskiem_ops_people_report(uuid)',
                           'taskiem_ops_people_held(text)',
                           'taskiem_ops_people_held_one(uuid)',
                           'taskiem_ops_people_verdicts(int)',
                           'taskiem_ops_people_appeals(text)',
                           'taskiem_ops_lawful_record(uuid, text, text, text, uuid, timestamptz, timestamptz, uuid, boolean, text, timestamptz, text)',
                           'taskiem_ops_lawful_requests(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_safety', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

ALTER TABLE lawful_requests DROP COLUMN scope_groups;

REVOKE INSERT ON group_messages FROM taskiem_safety;
DROP POLICY safety_event ON group_messages;
REVOKE UPDATE (replaced_at) ON group_links FROM taskiem_safety;
DROP POLICY safety_link_off ON group_links;
REVOKE SELECT (created_at, created_by, last_message_at), DELETE ON people_groups FROM taskiem_safety;
DROP TABLE group_standing;

DROP POLICY group_admin_read ON people_appeals;
DROP POLICY person_own ON people_appeals;
CREATE POLICY person_own ON people_appeals TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope() AND state = 'open'
    AND EXISTS (SELECT 1 FROM people_moderation m WHERE m.id = action_id AND m.person_id = taskiem_person_scope()));
DROP POLICY group_admin_read ON people_moderation;
DROP INDEX people_moderation_group;
-- Appeals of groups' actions go with them.
DELETE FROM people_moderation WHERE group_id IS NOT NULL;
ALTER TABLE people_moderation
  DROP CONSTRAINT people_moderation_whom,
  DROP CONSTRAINT people_moderation_check,
  ADD CONSTRAINT people_moderation_check CHECK (action NOT IN ('limit', 'close') OR until IS NOT NULL),
  DROP CONSTRAINT people_moderation_action_check,
  ADD CONSTRAINT people_moderation_action_check CHECK (action IN ('warn', 'limit', 'suspend', 'close')),
  DROP COLUMN group_id,
  ALTER COLUMN person_id SET NOT NULL;

ALTER TABLE group_messages DROP COLUMN screen;
REVOKE SELECT (group_id) ON people_screen_verdicts FROM taskiem_safety;
ALTER TABLE people_screen_verdicts DROP COLUMN group_id;

DROP POLICY person_writes ON people_held;
CREATE POLICY person_writes ON people_held FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND sender_id = taskiem_person_scope());
DELETE FROM people_held WHERE group_id IS NOT NULL;
DROP INDEX people_held_group;
ALTER TABLE people_held
  DROP CONSTRAINT people_held_where,
  DROP COLUMN mentions,
  DROP COLUMN group_id,
  ALTER COLUMN chat_id SET NOT NULL;

DROP POLICY person_writes ON people_reports;
CREATE POLICY person_writes ON people_reports FOR INSERT TO taskiem_app WITH CHECK (person_id = taskiem_person_scope());
DROP INDEX people_reports_group;
ALTER TABLE people_reports
  DROP CONSTRAINT people_reports_group,
  DROP COLUMN group_sealed,
  DROP COLUMN group_id;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_group_forget() RETURNS void
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_sweep(p_live interval)
RETURNS TABLE (what text, n bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a bigint;
  b bigint;
  c bigint;
BEGIN
  PERFORM taskiem_moderator_check();
  DELETE FROM people_gone WHERE at < now() - interval '1 hour';
  GET DIAGNOSTICS a = ROW_COUNT;
  DELETE FROM people_typing WHERE until < now();
  GET DIAGNOSTICS b = ROW_COUNT;
  DELETE FROM person_live WHERE seen_at < now() - p_live;
  GET DIAGNOSTICS c = ROW_COUNT;
  RETURN QUERY VALUES ('people_gone', a), ('people_typing', b), ('person_live', c);
END
$$;
-- +goose StatementEnd

DROP TABLE group_ledger;
