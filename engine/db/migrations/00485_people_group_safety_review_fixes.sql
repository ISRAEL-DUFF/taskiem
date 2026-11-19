-- People chatting with people, stage 5b: the review's fixes to safety in
-- groups ("As built: stage 5b, group safety (server)", Review fixes).
--
-- * people_reports.group_member: a report of one member of a group, kept
--   as such after the member's account is deleted (reported_id then goes
--   NULL); never shown or counted as a report of the whole group.
-- * A report from a group weighs half in the reported member's signals
--   (taskiem_people_sender_signals), and only when the server found it
--   earned (peer_wrote: the reporter's account past its first week, in
--   the group a day or more, and the member wrote within what the
--   reporter sees).
-- * people_held.client_id: a held send retried with the same client_id
--   is answered as held again, with no new held row or verdict
--   (taskiem_people_held_again).
-- * taskiem_ops_group_moderate: a report named with the action is one of
--   that group's.
-- * group_messages.screen is 'warn' only: a share card asking to be paid
--   is warned of per member when it is read (who hasn't added the
--   sender), never marked when it is sent.
-- +goose Up

ALTER TABLE people_reports
  ADD COLUMN group_member boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT people_reports_group_member CHECK (NOT group_member OR group_id IS NOT NULL);
UPDATE people_reports SET group_member = true WHERE group_id IS NOT NULL AND reported_id IS NOT NULL;

ALTER TABLE people_held ADD COLUMN client_id text CHECK (length(client_id) <= 80);
CREATE INDEX people_held_client ON people_held (sender_id, client_id) WHERE client_id IS NOT NULL;

UPDATE group_messages SET screen = 'warn' WHERE screen = 'transfer';
ALTER TABLE group_messages DROP CONSTRAINT group_messages_screen_check,
  ADD CONSTRAINT group_messages_screen_check CHECK (screen = 'warn');

-- A held send of the person in scope, by its client_id, in a chat (their
-- copy) or a group: the held row and its state, or nothing.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_held_again(p_chat uuid, p_group uuid, p_client text)
RETURNS TABLE (id uuid, state text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v uuid := taskiem_person_scope();
BEGIN
  IF v IS NULL OR taskiem_person_business() IS NOT NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  IF COALESCE(p_client, '') = '' OR (p_chat IS NULL) = (p_group IS NULL) THEN
    RAISE EXCEPTION 'a client_id, and a chat or a group' USING ERRCODE = '22023';
  END IF;
  RETURN QUERY
  SELECT h.id, h.state FROM people_held h
   WHERE h.sender_id = v AND h.client_id = p_client
     AND (h.chat_id = p_chat OR h.group_id = p_group)
   ORDER BY h.created_at LIMIT 1;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_people_held_again(uuid, uuid, text) OWNER TO taskiem_safety;
REVOKE EXECUTE ON FUNCTION taskiem_people_held_again(uuid, uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_held_again(uuid, uuid, text) TO taskiem_app;

-- The person's signals: a reporter counts once (as 00481), whole for a
-- one-to-one report, half when all their reports of the person in 30 days
-- came from groups; only reports the server found earned (peer_wrote).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_sender_signals()
RETURNS TABLE (reported_by int, device_accounts int)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v uuid := taskiem_person_scope();
BEGIN
  IF v IS NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT (SELECT COALESCE(floor(sum(w.weight)), 0)::int FROM (
            SELECT max(CASE WHEN q.group_id IS NULL THEN 1.0 ELSE 0.5 END) AS weight FROM people_reports q
             WHERE q.reported_id = v AND q.peer_wrote AND q.created_at > now() - interval '30 days'
             GROUP BY taskiem_people_reporter(q.reporter_mark, q.person_id, q.id)) w),
         taskiem_people_device_accounts(v);
END
$$;
-- +goose StatementEnd

-- A moderator's action on a group: as 00484's, and a report named with it
-- is one of the group's.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_group_moderate(p_group uuid, p_action text, p_reason text, p_by text, p_report uuid, p_until timestamptz) RETURNS uuid
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
  IF p_report IS NOT NULL AND NOT EXISTS (SELECT 1 FROM people_reports r WHERE r.id = p_report AND r.group_id = p_group) THEN
    RAISE EXCEPTION 'report_id is not a report of this group' USING ERRCODE = '22023';
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

-- The reports of a group or its members: as 00484's, with group_member.
DROP FUNCTION taskiem_ops_group_reports(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_group_reports(p_group uuid)
RETURNS TABLE (id uuid, reporter_id uuid, reported_id uuid, reported_name text, reason text, status text, created_at timestamptz, group_member boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.reported_id, COALESCE(p.name, ''), r.reason, r.status, r.created_at, r.group_member
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id
   WHERE r.group_id = p_group ORDER BY r.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

-- The reports' list: as 00484's, a whole group's reports told from a
-- member's by group_member (not by reported_id, which a deleted member's
-- report loses).
DROP FUNCTION taskiem_ops_people_reports(text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_reports(p_status text)
RETURNS TABLE (id uuid, reporter_id uuid, detached boolean, reported_id uuid, reported_name text, reported_gone boolean, reported_suspended boolean,
  reason text, note text, from_request boolean, files int, sealed boolean, status text, created_at timestamptz, reviewed_by text, reviewed_at timestamptz,
  reports_against bigint, group_id uuid, group_member boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.detached_at IS NOT NULL, r.reported_id, COALESCE(p.name, ''), COALESCE(p.deleted, false), COALESCE(p.suspended, false),
         r.reason, r.note, r.from_request, r.files, r.sealed IS NOT NULL, r.status, r.created_at, r.reviewed_by, r.reviewed_at,
         CASE WHEN r.group_id IS NOT NULL AND NOT r.group_member
                THEN (SELECT count(DISTINCT taskiem_people_reporter(q.reporter_mark, q.person_id, q.id)) FROM people_reports q
                       WHERE q.group_id = r.group_id AND NOT q.group_member)
              WHEN r.reported_id IS NULL THEN 0
              ELSE (SELECT count(DISTINCT taskiem_people_reporter(q.reporter_mark, q.person_id, q.id)) FROM people_reports q WHERE q.reported_id = r.reported_id) END,
         r.group_id, r.group_member
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id
   WHERE r.status = p_status OR p_status = 'all'
   ORDER BY r.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

-- One report: as 00484's, with group_member.
DROP FUNCTION taskiem_ops_people_report(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_report(p_id uuid)
RETURNS TABLE (id uuid, reporter_id uuid, reported_id uuid, reported_name text, reason text, note text, from_request boolean, messages jsonb, sealed bytea,
  box_key text, kms_key text, status text, created_at timestamptz, reviewed_by text, reviewed_at timestamptz, review_note text, group_id uuid,
  group_sealed bytea, group_member boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.reported_id, COALESCE(p.name, ''), r.reason, r.note, r.from_request, r.messages, r.sealed, r.box_key, r.kms_key,
         r.status, r.created_at, r.reviewed_by, r.reviewed_at, r.review_note, r.group_id, r.group_sealed, r.group_member
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id WHERE r.id = p_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_ops_group_reports(uuid)', 'taskiem_ops_people_reports(text)', 'taskiem_ops_people_report(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_safety', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- The functions as 00484 left them.
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

DROP FUNCTION taskiem_ops_group_reports(uuid);
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

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_ops_group_reports(uuid)', 'taskiem_ops_people_reports(text)', 'taskiem_ops_people_report(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_safety', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_group_moderate(p_group uuid, p_action text, p_reason text, p_by text, p_report uuid, p_until timestamptz) RETURNS uuid
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_sender_signals()
RETURNS TABLE (reported_by int, device_accounts int)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v uuid := taskiem_person_scope();
BEGIN
  IF v IS NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT (SELECT count(DISTINCT taskiem_people_reporter(q.reporter_mark, q.person_id, q.id))::int FROM people_reports q
           WHERE q.reported_id = v AND q.peer_wrote AND q.created_at > now() - interval '30 days'),
         taskiem_people_device_accounts(v);
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_people_held_again(uuid, uuid, text);

ALTER TABLE group_messages DROP CONSTRAINT group_messages_screen_check,
  ADD CONSTRAINT group_messages_screen_check CHECK (screen IN ('warn', 'transfer'));

DROP INDEX people_held_client;
ALTER TABLE people_held DROP COLUMN client_id;

ALTER TABLE people_reports DROP CONSTRAINT people_reports_group_member, DROP COLUMN group_member;
