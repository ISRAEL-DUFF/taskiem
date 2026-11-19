-- People chatting with people, stage 5a: the second review's fixes, the
-- server's part (docs/spec/people-chatting.md, "As built: stage 5a gaps",
-- "Second review fixes (server)"; decision 0069).
--
-- * The reach answers instead of raising (taskiem_people_reach_try): the
--   other person's state (ok, gone, suspended), or the caller's own
--   (self: their account is suspended), so a read, a reaction, delete for
--   everyone, mark paid, accept or typing goes on with the caller's own
--   copy in the same transaction. With p_forget the reach is given past a
--   suspension on either side (delete for everyone: a suspension stops
--   messaging, not forgetting; accepting a request). The raising
--   taskiem_people_reach also refuses a caller whose account is not
--   active (hint 'self').
-- * Closures: taskiem_people_closure_due locks the action and checks it is
--   still due (not lifted, not closed, its day come, no open appeal) in
--   the deletion's own transaction, which records it done there; a closure
--   stays in force (standing_sync) until the account is deleted. Lift and
--   appeal decisions take the same lock first.
-- * Abuse signals: a report keeps a keyed mark of its reporter
--   (reporter_mark, kept when the reporter is detached) and whether the
--   reported person had written to the reporter (peer_wrote); the sender's
--   signals count each reporter once, and only reports of people who
--   wrote to them.
-- * The safety tables (reports, held messages, actions, appeals, lawful
--   requests) are reached only by a role of their own, taskiem_safety
--   (NOLOGIN, a member of taskiem_dispatch, with no members), which owns
--   the moderators' and the safety job's definers; taskiem_dispatch no
--   longer reads or changes any of them. customer_reports' messages are
--   sealed at rest as people reports' are (old ones by the safety job).
-- * The published count of lawful requests shows only half-years that
--   have ended, and no "pending".
-- * people_search_ready keeps the backfill's progress (through_at,
--   through_id; done_at once finished).
-- * taskiem_people_sweep: the safety job prunes gone records older than an
--   hour, typing marks past their time and live connections not beaten
--   for a while, for everyone.
-- +goose Up

-- --- the reach answers ---

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
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = taskiem_person_scope() AND p.active AND NOT p.deleted) THEN
    RAISE EXCEPTION 'your account is suspended' USING ERRCODE = 'P0002', HINT = 'self';
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

-- The reach, answering: state is ok (in reach), none (no such chat of
-- the caller's), gone (the other's account was deleted), self (the
-- caller's own account is not active) or suspended (the other's is not).
-- reached says whether the other's copy is in reach: when ok, and with
-- p_forget also past a suspension on either side (never a deleted
-- account). Nothing raises for a state, so the caller's transaction goes
-- on with its own copy.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_reach_try(p_chat uuid, p_forget boolean)
RETURNS TABLE (peer uuid, state text, reached boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_person_scope();
  v_peer uuid;
  v_conv uuid;
  v_deleted boolean;
  v_active boolean;
  v_state text;
BEGIN
  IF v_me IS NULL THEN
    RAISE EXCEPTION 'a people chat is reached from a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  IF taskiem_person_business() IS NOT NULL OR cardinality(COALESCE(taskiem_tenant_scope(), '{}'::uuid[])) > 0 THEN
    RAISE EXCEPTION 'a business is in scope' USING ERRCODE = '42501';
  END IF;
  SELECT c.peer_id, c.conversation_id INTO v_peer, v_conv FROM people_chats c WHERE c.id = p_chat AND c.person_id = v_me;
  IF NOT FOUND OR v_peer IS NULL THEN
    RETURN QUERY SELECT NULL::uuid, 'none'::text, false;
    RETURN;
  END IF;
  SELECT p.deleted, p.active INTO v_deleted, v_active FROM person_profiles p WHERE p.person_id = v_peer;
  IF NOT FOUND OR v_deleted THEN
    RETURN QUERY SELECT v_peer, 'gone'::text, false;
    RETURN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = v_me AND p.active AND NOT p.deleted) THEN
    v_state := 'self';
  ELSIF NOT v_active THEN
    v_state := 'suspended';
  ELSE
    v_state := 'ok';
  END IF;
  IF v_state = 'ok' OR p_forget THEN
    PERFORM set_config('app.people_reach', v_conv::text, true);
    PERFORM set_config('app.people_peer', v_peer::text, true);
    RETURN QUERY SELECT v_peer, v_state, true;
    RETURN;
  END IF;
  RETURN QUERY SELECT v_peer, v_state, false;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_people_reach_try(uuid, boolean) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_people_reach_try(uuid, boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_reach_try(uuid, boolean) TO taskiem_app;

-- --- the safety tables' own role ---

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'taskiem_safety') THEN
    CREATE ROLE taskiem_safety NOLOGIN NOBYPASSRLS;
  END IF;
  -- The migrator hands it the definers below, which needs it to be able
  -- to become the role (a superuser always can).
  IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) AND NOT pg_has_role(current_user, 'taskiem_safety', 'MEMBER') THEN
    EXECUTE format('GRANT taskiem_safety TO %I', current_user);
  END IF;
END
$$;
-- +goose StatementEnd
-- It reads what any definer reads (people's names, sessions' devices, the
-- moderator check), and the safety tables besides; taskiem_dispatch, and
-- every login that is a member of it, reaches none of the safety tables.
GRANT taskiem_dispatch TO taskiem_safety;
GRANT USAGE ON SCHEMA public TO taskiem_safety;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['people_reports', 'customer_reports', 'people_report_files', 'people_held', 'people_moderation', 'people_appeals',
                           'lawful_requests'] LOOP
    EXECUTE format('DROP POLICY dispatch_all ON %I', t);
    EXECUTE format('REVOKE ALL ON %I FROM taskiem_dispatch', t);
    EXECUTE format('CREATE POLICY safety_all ON %I TO taskiem_safety USING (true) WITH CHECK (true)', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_safety', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- --- reports: their reporter counted once, customer reports sealed ---

ALTER TABLE people_reports
  ADD COLUMN reporter_mark bytea CHECK (reporter_mark IS NULL OR length(reporter_mark) = 32),
  ADD COLUMN peer_wrote boolean;
-- Reports made before: counted as before (the reported person is taken to
-- have written).
UPDATE people_reports SET peer_wrote = true;
ALTER TABLE people_reports ALTER COLUMN peer_wrote SET DEFAULT false, ALTER COLUMN peer_wrote SET NOT NULL;

ALTER TABLE customer_reports
  ADD COLUMN sealed        bytea,
  ADD COLUMN box_key       text,
  ADD COLUMN kms_key       text,
  ADD COLUMN message_count int,
  ADD CONSTRAINT customer_reports_box CHECK ((sealed IS NULL) = (box_key IS NULL) AND (box_key IS NULL) = (kms_key IS NULL));

-- How many reporters: each once, by their mark (kept past their account),
-- else their id, else (an old detached report) the report.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_reporter(p_mark bytea, p_person uuid, p_id uuid) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT COALESCE('m' || encode(p_mark, 'hex'), 'p' || p_person::text, 'r' || p_id::text)
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_people_reporter(bytea, uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_reporter(bytea, uuid, uuid) TO taskiem_safety;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_people_reports(p_status text)
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_people_person(p_person uuid)
RETURNS TABLE (person_id uuid, name text, active boolean, deleted boolean, suspended boolean, created_at timestamptz, reports_against bigint,
  held bigint, verdicts_week bigint, device_accounts int, requests_waiting bigint, requests_accepted bigint, requests_refused bigint)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT p.person_id, p.name, p.active, p.deleted, p.suspended, u.created_at,
         (SELECT count(DISTINCT taskiem_people_reporter(q.reporter_mark, q.person_id, q.id)) FROM people_reports q WHERE q.reported_id = p.person_id),
         (SELECT count(*) FROM people_held h WHERE h.sender_id = p.person_id),
         (SELECT count(*) FROM people_screen_verdicts v WHERE v.from_person = p.person_id AND v.at > now() - interval '7 days'),
         taskiem_people_device_accounts(p.person_id),
         (SELECT count(*) FROM people_requests r WHERE r.sender_id = p.person_id AND r.outcome = 'waiting'),
         (SELECT count(*) FROM people_requests r WHERE r.sender_id = p.person_id AND r.outcome = 'accepted'),
         (SELECT count(*) FROM people_requests r WHERE r.sender_id = p.person_id AND r.outcome IN ('deleted', 'blocked'))
    FROM person_profiles p JOIN users u ON u.id = p.person_id WHERE p.person_id = p_person;
END
$$;
-- +goose StatementEnd

-- The sender's signals: each reporter once in 30 days, by their mark
-- (a reporter who deletes their account is still one), and only reports
-- of a person who wrote to the reporter: reporting someone who never
-- wrote to you, however often, from however many accounts made for it,
-- weighs nothing.
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

-- Deleting an account: as before, and the reporter's mark is kept on
-- their reports (p_mark, the server's keyed mark of them) where the
-- report has none yet.
DROP FUNCTION taskiem_people_reports_detach(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_reports_detach(p_person uuid, p_mark bytea) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
  m bigint;
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'for the person''s own account' USING ERRCODE = '42501';
  END IF;
  IF p_mark IS NULL OR length(p_mark) <> 32 THEN
    RAISE EXCEPTION 'a reporter''s mark is 32 bytes' USING ERRCODE = '22023';
  END IF;
  UPDATE people_reports SET person_id = NULL, detached_at = now(), reporter_mark = COALESCE(reporter_mark, p_mark) WHERE person_id = p_person;
  GET DIAGNOSTICS n = ROW_COUNT;
  UPDATE customer_reports SET person_id = NULL, detached_at = now() WHERE person_id = p_person;
  GET DIAGNOSTICS m = ROW_COUNT;
  UPDATE people_report_files SET reporter_id = NULL WHERE reporter_id = p_person;
  DELETE FROM people_held WHERE sender_id = p_person AND state = 'held';
  UPDATE people_held SET sender_id = NULL WHERE sender_id = p_person;
  UPDATE people_held SET recipient_id = NULL WHERE recipient_id = p_person;
  RETURN n + m;
END
$$;
-- +goose StatementEnd

-- customer_reports: the count without the words, and the sealed messages
-- with their key (opened by the server, audited with why).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_customer_reports(p_status text)
RETURNS TABLE (id uuid, reporter_id uuid, detached boolean, tenant_id uuid, business text, reason text, note text, messages int, status text,
  created_at timestamptz, reviewed_by text, reviewed_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.detached_at IS NOT NULL, r.tenant_id, t.name, r.reason, r.note, COALESCE(r.message_count, jsonb_array_length(r.messages)),
         r.status, r.created_at, r.reviewed_by, r.reviewed_at
    FROM customer_reports r JOIN tenants t ON t.id = r.tenant_id
   WHERE r.status = p_status OR p_status = 'all'
   ORDER BY r.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_customer_report(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_customer_report(p_id uuid)
RETURNS TABLE (id uuid, reporter_id uuid, tenant_id uuid, business text, reason text, note text, messages jsonb, sealed bytea, box_key text,
  kms_key text, status text, created_at timestamptz, reviewed_by text, reviewed_at timestamptz, review_note text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.tenant_id, t.name, r.reason, r.note, r.messages, r.sealed, r.box_key, r.kms_key, r.status, r.created_at, r.reviewed_by,
         r.reviewed_at, r.review_note
    FROM customer_reports r JOIN tenants t ON t.id = r.tenant_id WHERE r.id = p_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_customer_reports_unsealed(p_limit int)
RETURNS TABLE (id uuid, messages jsonb)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY SELECT r.id, r.messages FROM customer_reports r WHERE r.sealed IS NULL ORDER BY r.created_at LIMIT LEAST(p_limit, 500);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_customer_report_seal(p_id uuid, p_sealed bytea, p_box text, p_kms text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  UPDATE customer_reports SET sealed = p_sealed, box_key = p_box, kms_key = p_kms, message_count = jsonb_array_length(messages), messages = '[]'::jsonb
   WHERE id = p_id AND sealed IS NULL;
  RETURN FOUND;
END
$$;
-- +goose StatementEnd

-- --- closures carried out only when done, and locked against a late lift ---

-- A suspension, or a closure until the account is deleted (a closure that
-- could not be carried out stays in force, to be tried again).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_standing_sync(p_person uuid) RETURNS boolean
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  v boolean;
BEGIN
  v := EXISTS (SELECT 1 FROM people_moderation m WHERE m.person_id = p_person AND m.lifted_at IS NULL
                AND ((m.action = 'close' AND (m.closed_at IS NULL OR EXISTS (SELECT 1 FROM users u WHERE u.id = p_person AND u.deleted_at IS NULL)))
                  OR (m.action = 'suspend' AND m.closed_at IS NULL AND (m.until IS NULL OR m.until > now()))));
  UPDATE person_profiles p SET suspended = v,
         active = NOT v AND EXISTS (SELECT 1 FROM users u WHERE u.id = p_person AND u.status = 'active' AND u.deleted_at IS NULL), updated_at = now()
   WHERE p.person_id = p_person;
  RETURN v;
END
$$;
-- +goose StatementEnd

-- In the deletion's own transaction (the platform chain in scope): locks
-- the closure and answers whether it is still due: not lifted, not
-- carried out, its day come and no appeal open. A lift, an appeal or its
-- decision takes the same lock, so either comes first and the other sees
-- it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_closure_due(p_action uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  m people_moderation%ROWTYPE;
BEGIN
  PERFORM taskiem_moderator_check();
  SELECT * INTO m FROM people_moderation WHERE id = p_action FOR UPDATE;
  IF NOT FOUND OR m.action <> 'close' OR m.lifted_at IS NOT NULL OR m.closed_at IS NOT NULL OR m.until > now() THEN
    RETURN false;
  END IF;
  -- A statement of its own, after the lock: an appeal made while this
  -- waited for it is seen.
  RETURN NOT EXISTS (SELECT 1 FROM people_appeals a WHERE a.action_id = p_action AND a.state = 'open');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_people_lift(p_action uuid, p_by text, p_reason text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_person uuid;
BEGIN
  PERFORM taskiem_moderator_check();
  -- The closure's lock first (taskiem_people_closure_due).
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_people_appeal_decide(p_id uuid, p_state text, p_by text, p_note text) RETURNS uuid
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
  -- The action's lock first (taskiem_people_closure_due): a closure being
  -- carried out is finished before its appeal is decided, and the other
  -- way round.
  SELECT m.closed_at INTO v_closed FROM people_moderation m WHERE m.id = (SELECT a.action_id FROM people_appeals a WHERE a.id = p_id) FOR UPDATE;
  -- The person is told a different moderator reads their appeal.
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

-- --- the published count: ended half-years only ---

DROP FUNCTION taskiem_lawful_request_counts();
-- +goose StatementBegin
CREATE FUNCTION taskiem_lawful_request_counts()
RETURNS TABLE (year int, half int, received bigint, answered bigint, refused bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  -- A half-year is published once it has ended (Lagos time), so the count
  -- never moves while a request is new, which could tell its subject.
  SELECT y, h, count(*), count(*) FILTER (WHERE state = 'answered'), count(*) FILTER (WHERE state = 'refused')
    FROM (SELECT extract(year FROM received_at AT TIME ZONE 'Africa/Lagos')::int AS y,
                 CASE WHEN extract(month FROM received_at AT TIME ZONE 'Africa/Lagos') <= 6 THEN 1 ELSE 2 END AS h, state
            FROM lawful_requests) l
   WHERE (y, h) < (extract(year FROM now() AT TIME ZONE 'Africa/Lagos')::int,
                   CASE WHEN extract(month FROM now() AT TIME ZONE 'Africa/Lagos') <= 6 THEN 1 ELSE 2 END)
   GROUP BY 1, 2 ORDER BY 1 DESC, 2 DESC
$$;
-- +goose StatementEnd

-- --- the safety definers, owned by taskiem_safety ---

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_ops_people_reports(text)',
                           'taskiem_ops_people_report(uuid)',
                           'taskiem_ops_people_report_files(uuid)',
                           'taskiem_ops_people_report_file(uuid, uuid)',
                           'taskiem_ops_people_report_seal(uuid, bytea, text, text)',
                           'taskiem_ops_people_reports_unsealed(int)',
                           'taskiem_ops_customer_reports(text)',
                           'taskiem_ops_customer_report(uuid)',
                           'taskiem_ops_customer_reports_unsealed(int)',
                           'taskiem_ops_customer_report_seal(uuid, bytea, text, text)',
                           'taskiem_ops_report_review(text, uuid, text, text)',
                           'taskiem_ops_people_held(text)',
                           'taskiem_ops_people_held_one(uuid)',
                           'taskiem_ops_people_held_decide(uuid, text, text, text, uuid)',
                           'taskiem_ops_people_verdicts(int)',
                           'taskiem_ops_people_person(uuid)',
                           'taskiem_ops_people_moderate(uuid, text, text, text, uuid, timestamptz)',
                           'taskiem_ops_people_lift(uuid, text, text)',
                           'taskiem_ops_people_actions(uuid)',
                           'taskiem_ops_people_appeals(text)',
                           'taskiem_ops_people_appeal_decide(uuid, text, text, text)',
                           'taskiem_people_standing_tick()',
                           'taskiem_people_closure_due(uuid)',
                           'taskiem_people_closure_done(uuid, text)',
                           'taskiem_people_safety_purge(interval, interval)',
                           'taskiem_people_reports_detach(uuid, bytea)',
                           'taskiem_people_sender_signals()',
                           'taskiem_ops_lawful_record(uuid, text, text, text, uuid, timestamptz, timestamptz, uuid, boolean, text, timestamptz, text)',
                           'taskiem_ops_lawful_requests(uuid)',
                           'taskiem_ops_lawful_step(uuid, text, text, int, text, timestamptz)',
                           'taskiem_lawful_request_counts()'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_safety', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
  -- Not the app's: the definers above call it.
  EXECUTE 'ALTER FUNCTION taskiem_people_standing_sync(uuid) OWNER TO taskiem_safety';
  EXECUTE 'REVOKE EXECUTE ON FUNCTION taskiem_people_standing_sync(uuid) FROM PUBLIC, taskiem_dispatch';
END $$;
-- +goose StatementEnd

-- --- search backfill progress ---

ALTER TABLE people_search_ready
  ALTER COLUMN done_at DROP NOT NULL,
  ALTER COLUMN done_at DROP DEFAULT,
  ADD COLUMN through_at timestamptz,
  ADD COLUMN through_id uuid;

-- --- the sweep ---

-- The safety job's sweep (the platform chain in scope): gone records
-- older than an hour, typing marks past their time and live connections
-- not beaten for p_live, for everyone. Answers how many went.
CREATE POLICY dispatch_sweep ON people_gone TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_sweep ON people_typing TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_sweep ON person_live TO taskiem_dispatch USING (true);
GRANT SELECT (at), DELETE ON people_gone TO taskiem_dispatch;
GRANT SELECT (until), DELETE ON people_typing TO taskiem_dispatch;
GRANT SELECT (seen_at), DELETE ON person_live TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_sweep(p_live interval)
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
ALTER FUNCTION taskiem_people_sweep(interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_people_sweep(interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_sweep(interval) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_people_sweep(interval);
REVOKE ALL ON people_gone, people_typing, person_live FROM taskiem_dispatch;
DROP POLICY dispatch_sweep ON people_gone;
DROP POLICY dispatch_sweep ON people_typing;
DROP POLICY dispatch_sweep ON person_live;

DELETE FROM people_search_ready WHERE done_at IS NULL;
ALTER TABLE people_search_ready
  DROP COLUMN through_id,
  DROP COLUMN through_at,
  ALTER COLUMN done_at SET DEFAULT now(),
  ALTER COLUMN done_at SET NOT NULL;

-- The definers go back to taskiem_dispatch, and the tables with them.
-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_ops_people_reports(text)',
                           'taskiem_ops_people_report(uuid)',
                           'taskiem_ops_people_report_files(uuid)',
                           'taskiem_ops_people_report_file(uuid, uuid)',
                           'taskiem_ops_people_report_seal(uuid, bytea, text, text)',
                           'taskiem_ops_people_reports_unsealed(int)',
                           'taskiem_ops_customer_reports(text)',
                           'taskiem_ops_customer_report(uuid)',
                           'taskiem_ops_customer_reports_unsealed(int)',
                           'taskiem_ops_customer_report_seal(uuid, bytea, text, text)',
                           'taskiem_ops_report_review(text, uuid, text, text)',
                           'taskiem_ops_people_held(text)',
                           'taskiem_ops_people_held_one(uuid)',
                           'taskiem_ops_people_held_decide(uuid, text, text, text, uuid)',
                           'taskiem_ops_people_verdicts(int)',
                           'taskiem_ops_people_person(uuid)',
                           'taskiem_ops_people_moderate(uuid, text, text, text, uuid, timestamptz)',
                           'taskiem_ops_people_lift(uuid, text, text)',
                           'taskiem_ops_people_actions(uuid)',
                           'taskiem_ops_people_appeals(text)',
                           'taskiem_ops_people_appeal_decide(uuid, text, text, text)',
                           'taskiem_people_standing_tick()',
                           'taskiem_people_closure_due(uuid)',
                           'taskiem_people_closure_done(uuid, text)',
                           'taskiem_people_safety_purge(interval, interval)',
                           'taskiem_people_reports_detach(uuid, bytea)',
                           'taskiem_people_sender_signals()',
                           'taskiem_ops_lawful_record(uuid, text, text, text, uuid, timestamptz, timestamptz, uuid, boolean, text, timestamptz, text)',
                           'taskiem_ops_lawful_requests(uuid)',
                           'taskiem_ops_lawful_step(uuid, text, text, int, text, timestamptz)',
                           'taskiem_lawful_request_counts()',
                           'taskiem_people_standing_sync(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
  END LOOP;
  EXECUTE 'GRANT EXECUTE ON FUNCTION taskiem_people_standing_sync(uuid) TO taskiem_dispatch';
  FOREACH f IN ARRAY ARRAY['people_reports', 'customer_reports', 'people_report_files', 'people_held', 'people_moderation', 'people_appeals',
                           'lawful_requests'] LOOP
    EXECUTE format('DROP POLICY safety_all ON %I', f);
    EXECUTE format('REVOKE ALL ON %I FROM taskiem_safety', f);
    EXECUTE format('CREATE POLICY dispatch_all ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', f);
  END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT (id, person_id, reported_id, reason, note, messages, from_request, status, created_at, sealed, box_key, kms_key, files, detached_at,
  reviewed_by, reviewed_at, review_note), UPDATE, DELETE ON people_reports TO taskiem_dispatch;
GRANT SELECT (id, person_id, tenant_id, reason, note, messages, status, created_at, detached_at, reviewed_by, reviewed_at, review_note), UPDATE, DELETE
  ON customer_reports TO taskiem_dispatch;
GRANT SELECT (report_id, message_id, reporter_id, type, size, sealed, created_at), UPDATE (reporter_id), DELETE ON people_report_files TO taskiem_dispatch;
GRANT SELECT (id, sender_id, recipient_id, chat_id, kind, reply_to, forwards, reasons, sealed, file_sealed, file_type, file_size, box_key, kms_key,
  state, decided_by, decided_at, decision_note, released_message, created_at), UPDATE, DELETE ON people_held TO taskiem_dispatch;
GRANT SELECT (id, person_id, action, reason, operator, report_id, until, lifted_at, lifted_by, lift_reason, closed_at, close_note, seen_at, created_at),
  INSERT, UPDATE ON people_moderation TO taskiem_dispatch;
GRANT SELECT (id, action_id, person_id, text, state, decided_by, decided_at, decision_note, created_at), UPDATE ON people_appeals TO taskiem_dispatch;
GRANT SELECT (id, authority, reference, legal_basis, person_id, scope_from, scope_to, scope_with, scope_files, scope_note, received_at, recorded_by,
  recorded_at, approved_by, approved_at, state, answered_by, answered_at, messages_given, refused_reason, told_at, not_told_reason),
  INSERT, UPDATE ON lawful_requests TO taskiem_dispatch;
REVOKE taskiem_dispatch FROM taskiem_safety;
REVOKE USAGE ON SCHEMA public FROM taskiem_safety;

-- 00480's bodies.
DROP FUNCTION taskiem_lawful_request_counts();
-- +goose StatementBegin
CREATE FUNCTION taskiem_lawful_request_counts()
RETURNS TABLE (year int, half int, received bigint, answered bigint, refused bigint, pending bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT extract(year FROM received_at AT TIME ZONE 'Africa/Lagos')::int,
         CASE WHEN extract(month FROM received_at AT TIME ZONE 'Africa/Lagos') <= 6 THEN 1 ELSE 2 END,
         count(*), count(*) FILTER (WHERE state = 'answered'), count(*) FILTER (WHERE state = 'refused'),
         count(*) FILTER (WHERE state IN ('recorded', 'approved'))
    FROM lawful_requests GROUP BY 1, 2 ORDER BY 1 DESC, 2 DESC
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_lawful_request_counts() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_lawful_request_counts() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_lawful_request_counts() TO taskiem_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_people_appeal_decide(p_id uuid, p_state text, p_by text, p_note text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_person uuid;
  v_action uuid;
BEGIN
  PERFORM taskiem_moderator_check();
  IF p_state NOT IN ('upheld', 'overturned') OR COALESCE(p_by, '') = '' THEN
    RAISE EXCEPTION 'upheld or overturned, by a moderator' USING ERRCODE = '22023';
  END IF;
  IF EXISTS (SELECT 1 FROM people_appeals a JOIN people_moderation m ON m.id = a.action_id WHERE a.id = p_id AND m.operator = p_by) THEN
    RAISE EXCEPTION 'a different moderator answers an appeal: not the one who decided' USING ERRCODE = '42501';
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_people_lift(p_action uuid, p_by text, p_reason text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_person uuid;
BEGIN
  PERFORM taskiem_moderator_check();
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

DROP FUNCTION taskiem_people_closure_due(uuid);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_standing_sync(p_person uuid) RETURNS boolean
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  v boolean;
BEGIN
  v := EXISTS (SELECT 1 FROM people_moderation m WHERE m.person_id = p_person AND m.lifted_at IS NULL AND m.closed_at IS NULL
                AND (m.action = 'close' OR (m.action = 'suspend' AND (m.until IS NULL OR m.until > now()))));
  UPDATE person_profiles p SET suspended = v,
         active = NOT v AND EXISTS (SELECT 1 FROM users u WHERE u.id = p_person AND u.status = 'active' AND u.deleted_at IS NULL), updated_at = now()
   WHERE p.person_id = p_person;
  RETURN v;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_ops_customer_report_seal(uuid, bytea, text, text);
DROP FUNCTION taskiem_ops_customer_reports_unsealed(int);
DROP FUNCTION taskiem_ops_customer_report(uuid);
-- A sealed report of a business cannot be opened in SQL: it goes down
-- with its key.
DELETE FROM customer_reports WHERE sealed IS NOT NULL;
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_customer_report(p_id uuid)
RETURNS TABLE (id uuid, reporter_id uuid, tenant_id uuid, business text, reason text, note text, messages jsonb, status text, created_at timestamptz,
  reviewed_by text, reviewed_at timestamptz, review_note text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.tenant_id, t.name, r.reason, r.note, r.messages, r.status, r.created_at, r.reviewed_by, r.reviewed_at, r.review_note
    FROM customer_reports r JOIN tenants t ON t.id = r.tenant_id WHERE r.id = p_id;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_customer_report(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_customer_report(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_customer_report(uuid) TO taskiem_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_customer_reports(p_status text)
RETURNS TABLE (id uuid, reporter_id uuid, detached boolean, tenant_id uuid, business text, reason text, note text, messages int, status text,
  created_at timestamptz, reviewed_by text, reviewed_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.detached_at IS NOT NULL, r.tenant_id, t.name, r.reason, r.note, jsonb_array_length(r.messages), r.status, r.created_at,
         r.reviewed_by, r.reviewed_at
    FROM customer_reports r JOIN tenants t ON t.id = r.tenant_id
   WHERE r.status = p_status OR p_status = 'all'
   ORDER BY r.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

ALTER TABLE customer_reports
  DROP CONSTRAINT customer_reports_box,
  DROP COLUMN message_count, DROP COLUMN kms_key, DROP COLUMN box_key, DROP COLUMN sealed;

DROP FUNCTION taskiem_people_reports_detach(uuid, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_reports_detach(p_person uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
  m bigint;
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'for the person''s own account' USING ERRCODE = '42501';
  END IF;
  UPDATE people_reports SET person_id = NULL, detached_at = now() WHERE person_id = p_person;
  GET DIAGNOSTICS n = ROW_COUNT;
  UPDATE customer_reports SET person_id = NULL, detached_at = now() WHERE person_id = p_person;
  GET DIAGNOSTICS m = ROW_COUNT;
  UPDATE people_report_files SET reporter_id = NULL WHERE reporter_id = p_person;
  DELETE FROM people_held WHERE sender_id = p_person AND state = 'held';
  UPDATE people_held SET sender_id = NULL WHERE sender_id = p_person;
  UPDATE people_held SET recipient_id = NULL WHERE recipient_id = p_person;
  RETURN n + m;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_people_reports_detach(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_people_reports_detach(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_reports_detach(uuid) TO taskiem_app;

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
  SELECT (SELECT count(DISTINCT COALESCE(q.person_id, q.id))::int FROM people_reports q WHERE q.reported_id = v AND q.created_at > now() - interval '30 days'),
         taskiem_people_device_accounts(v);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_people_person(p_person uuid)
RETURNS TABLE (person_id uuid, name text, active boolean, deleted boolean, suspended boolean, created_at timestamptz, reports_against bigint,
  held bigint, verdicts_week bigint, device_accounts int, requests_waiting bigint, requests_accepted bigint, requests_refused bigint)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT p.person_id, p.name, p.active, p.deleted, p.suspended, u.created_at,
         (SELECT count(DISTINCT COALESCE(q.person_id, q.id)) FROM people_reports q WHERE q.reported_id = p.person_id),
         (SELECT count(*) FROM people_held h WHERE h.sender_id = p.person_id),
         (SELECT count(*) FROM people_screen_verdicts v WHERE v.from_person = p.person_id AND v.at > now() - interval '7 days'),
         taskiem_people_device_accounts(p.person_id),
         (SELECT count(*) FROM people_requests r WHERE r.sender_id = p.person_id AND r.outcome = 'waiting'),
         (SELECT count(*) FROM people_requests r WHERE r.sender_id = p.person_id AND r.outcome = 'accepted'),
         (SELECT count(*) FROM people_requests r WHERE r.sender_id = p.person_id AND r.outcome IN ('deleted', 'blocked'))
    FROM person_profiles p JOIN users u ON u.id = p.person_id WHERE p.person_id = p_person;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_people_reports(p_status text)
RETURNS TABLE (id uuid, reporter_id uuid, detached boolean, reported_id uuid, reported_name text, reported_gone boolean, reported_suspended boolean,
  reason text, note text, from_request boolean, files int, sealed boolean, status text, created_at timestamptz, reviewed_by text, reviewed_at timestamptz,
  reports_against bigint)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY
  SELECT r.id, r.person_id, r.detached_at IS NOT NULL, r.reported_id, COALESCE(p.name, ''), COALESCE(p.deleted, false), COALESCE(p.suspended, false),
         r.reason, r.note, r.from_request, r.files, r.sealed IS NOT NULL, r.status, r.created_at, r.reviewed_by, r.reviewed_at,
         (SELECT count(DISTINCT COALESCE(q.person_id, q.id)) FROM people_reports q WHERE q.reported_id = r.reported_id)
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id
   WHERE r.status = p_status OR p_status = 'all'
   ORDER BY r.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_people_reporter(bytea, uuid, uuid);
ALTER TABLE people_reports DROP COLUMN peer_wrote, DROP COLUMN reporter_mark;

DROP FUNCTION taskiem_people_reach_try(uuid, boolean);
-- 00477's body.
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
