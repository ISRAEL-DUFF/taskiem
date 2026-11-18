-- People chatting with people, stage 5a: safety, moderation and the
-- operator's tools (docs/spec/people-chatting.md §6.3, §8, §14, §16;
-- "As built: stage 5a gaps, safety and moderation"; decision 0069 points
-- 3, 5 and 11).
--
-- * Reports keep their evidence. people_reports gains its own data key
--   (box_key, sealed with the platform's root key for that report alone)
--   and the last 20 messages sealed under it (sealed); messages stays as
--   '[]' for a sealed report (older plain reports are sealed by the safety
--   job). people_report_files holds the photos and files of those
--   messages, copied and sealed under the report's key at report time. A
--   report outlives its reporter: person_id is set to NULL when the
--   reporter deletes their account (detached_at), as is customer_reports'.
--   Reviewed reports are kept a year after review, then purged.
-- * Held messages are kept for moderators: people_held, the message a
--   check held, sealed under a key of its own, with its verdict and
--   reasons; moderators release it (delivered then, as the sender meant)
--   or confirm it. Purged 90 days after it was held or decided.
-- * Moderators' actions on a person (people_moderation: warn, limit,
--   suspend, close), with reason, operator and time, read by the person
--   (their standing), and appeals (people_appeals) the person writes and
--   moderators uphold or overturn. A suspension or closure sets
--   person_profiles.suspended, which keeps active false (the suspended
--   state 00477 and 00478 read) whatever users.status says.
-- * Lawful requests (lawful_requests): recorded by one operator, approved
--   by another, answered with exactly the scope asked; whether and when
--   the person was told. Only counts leave the console
--   (taskiem_lawful_request_counts, the public page).
-- * Abuse signals for the request allowance: reports against a sender and
--   how many accounts share their devices (taskiem_people_sender_signals).
--
-- Every moderator's function checks the platform chain is in scope (the
-- operator console's transaction, api/ops_console.go opsTx), as
-- taskiem_discover_moderate does.
-- +goose Up

-- +goose StatementBegin
CREATE FUNCTION taskiem_moderator_scope() RETURNS boolean
LANGUAGE sql STABLE PARALLEL SAFE AS $$
  SELECT 'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid = ANY (taskiem_tenant_scope())
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION taskiem_moderator_scope() TO taskiem_app, taskiem_dispatch;

-- --- reports keep their evidence ---

ALTER TABLE people_reports
  ALTER COLUMN person_id DROP NOT NULL,
  DROP CONSTRAINT people_reports_person_id_fkey,
  ADD CONSTRAINT people_reports_person_id_fkey FOREIGN KEY (person_id) REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN sealed      bytea,
  ADD COLUMN box_key     text,
  ADD COLUMN kms_key     text,
  ADD COLUMN files       int NOT NULL DEFAULT 0,
  ADD COLUMN detached_at timestamptz,
  ADD COLUMN reviewed_by text,
  ADD COLUMN reviewed_at timestamptz,
  ADD COLUMN review_note text CHECK (review_note IS NULL OR length(review_note) <= 1000),
  ADD CONSTRAINT people_reports_box CHECK ((sealed IS NULL) = (box_key IS NULL) AND (box_key IS NULL) = (kms_key IS NULL));
CREATE INDEX people_reports_reported ON people_reports (reported_id, created_at) WHERE reported_id IS NOT NULL;
CREATE INDEX people_reports_reviewed ON people_reports (reviewed_at) WHERE status = 'reviewed';
CREATE POLICY dispatch_all ON people_reports TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (id, person_id, reported_id, reason, note, messages, from_request, status, created_at, sealed, box_key, kms_key, files, detached_at,
  reviewed_by, reviewed_at, review_note), UPDATE, DELETE ON people_reports TO taskiem_dispatch;

ALTER TABLE customer_reports
  ALTER COLUMN person_id DROP NOT NULL,
  DROP CONSTRAINT customer_reports_person_id_fkey,
  ADD CONSTRAINT customer_reports_person_id_fkey FOREIGN KEY (person_id) REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN detached_at timestamptz,
  ADD COLUMN reviewed_by text,
  ADD COLUMN reviewed_at timestamptz,
  ADD COLUMN review_note text CHECK (review_note IS NULL OR length(review_note) <= 1000);
CREATE INDEX customer_reports_reviewed ON customer_reports (reviewed_at) WHERE status = 'reviewed';
CREATE POLICY dispatch_all ON customer_reports TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (id, person_id, tenant_id, reason, note, messages, status, created_at, detached_at, reviewed_by, reviewed_at, review_note), UPDATE, DELETE
  ON customer_reports TO taskiem_dispatch;

-- The photos and files of a report's messages, copied at report time and
-- sealed under the report's key (row "f:{message}"), so the report keeps
-- them after the chat or the account goes.
CREATE TABLE people_report_files (
  report_id    uuid NOT NULL REFERENCES people_reports(id) ON DELETE CASCADE,
  message_id   uuid NOT NULL,
  reporter_id  uuid REFERENCES users(id) ON DELETE SET NULL,
  type         text NOT NULL CHECK (length(type) <= 100),
  size         bigint NOT NULL CHECK (size > 0 AND size <= 26214400),
  sealed       bytea NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (report_id, message_id)
);
ALTER TABLE people_report_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_report_files FORCE ROW LEVEL SECURITY;
CREATE POLICY person_writes ON people_report_files FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND reporter_id = taskiem_person_scope());
CREATE POLICY dispatch_all ON people_report_files TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON people_report_files FROM PUBLIC;
GRANT INSERT ON people_report_files TO taskiem_app;
GRANT SELECT (report_id, message_id, reporter_id, type, size, sealed, created_at), UPDATE (reporter_id), DELETE ON people_report_files TO taskiem_dispatch;

-- --- held messages kept for review ---

CREATE TABLE people_held (
  id               uuid PRIMARY KEY,
  sender_id        uuid REFERENCES users(id) ON DELETE SET NULL,
  recipient_id     uuid REFERENCES users(id) ON DELETE SET NULL,
  -- The sender's copy of the chat it was written in.
  chat_id          uuid NOT NULL,
  kind             text NOT NULL CHECK (kind IN ('text', 'media', 'card', 'location')),
  reply_to         uuid,
  forwards         int NOT NULL DEFAULT 0,
  reasons          text[] NOT NULL DEFAULT '{}',
  -- The message (its words, card, place, file name), and its file, sealed
  -- under the held message's own key (rows "m" and "f").
  sealed           bytea NOT NULL,
  file_sealed      bytea,
  file_type        text CHECK (file_type IS NULL OR length(file_type) <= 100),
  file_size        bigint,
  box_key          text NOT NULL,
  kms_key          text NOT NULL,
  state            text NOT NULL DEFAULT 'held' CHECK (state IN ('held', 'released', 'confirmed')),
  decided_by       text,
  decided_at       timestamptz,
  decision_note    text CHECK (decision_note IS NULL OR length(decision_note) <= 1000),
  released_message uuid,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CHECK ((file_sealed IS NULL) = (file_type IS NULL) AND (file_type IS NULL) = (file_size IS NULL))
);
CREATE INDEX people_held_state ON people_held (state, created_at);
CREATE INDEX people_held_sender ON people_held (sender_id) WHERE sender_id IS NOT NULL;
ALTER TABLE people_held ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_held FORCE ROW LEVEL SECURITY;
CREATE POLICY person_writes ON people_held FOR INSERT TO taskiem_app
  WITH CHECK (taskiem_person_business() IS NULL AND sender_id = taskiem_person_scope());
CREATE POLICY dispatch_all ON people_held TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON people_held FROM PUBLIC;
GRANT INSERT ON people_held TO taskiem_app;
GRANT SELECT (id, sender_id, recipient_id, chat_id, kind, reply_to, forwards, reasons, sealed, file_sealed, file_type, file_size, box_key, kms_key,
  state, decided_by, decided_at, decision_note, released_message, created_at), UPDATE, DELETE ON people_held TO taskiem_dispatch;

-- Moderators read the checks' verdicts (never words).
GRANT SELECT (verdict, reasons, at) ON people_screen_verdicts TO taskiem_dispatch;

-- --- moderators' actions and appeals ---

ALTER TABLE person_profiles ADD COLUMN suspended boolean NOT NULL DEFAULT false;
GRANT SELECT (suspended), UPDATE (suspended) ON person_profiles TO taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_person_profile_sync() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  -- A moderator's suspension keeps the account inactive in people chats
  -- whatever users.status says.
  INSERT INTO person_profiles (person_id, name, active, deleted)
  VALUES (NEW.id, COALESCE(NEW.name, ''), NEW.status = 'active', NEW.deleted_at IS NOT NULL)
  ON CONFLICT (person_id) DO UPDATE SET name = EXCLUDED.name, active = EXCLUDED.active AND NOT person_profiles.suspended,
    deleted = EXCLUDED.deleted, updated_at = now();
  RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TABLE people_moderation (
  id           uuid PRIMARY KEY,
  person_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  action       text NOT NULL CHECK (action IN ('warn', 'limit', 'suspend', 'close')),
  reason       text NOT NULL CHECK (length(reason) BETWEEN 1 AND 1000),
  operator     text NOT NULL CHECK (operator <> ''),
  report_id    uuid,
  -- A limit's or suspension's end (NULL: until lifted); a closure's day
  -- (the account is deleted then, unless an appeal overturns it).
  until        timestamptz,
  lifted_at    timestamptz,
  lifted_by    text,
  lift_reason  text CHECK (lift_reason IS NULL OR length(lift_reason) <= 1000),
  closed_at    timestamptz,
  close_note   text,
  seen_at      timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  CHECK (action NOT IN ('limit', 'close') OR until IS NOT NULL)
);
CREATE INDEX people_moderation_person ON people_moderation (person_id, created_at);
CREATE INDEX people_moderation_live ON people_moderation (until) WHERE lifted_at IS NULL AND closed_at IS NULL;
ALTER TABLE people_moderation ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_moderation FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON people_moderation TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
CREATE POLICY dispatch_all ON people_moderation TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON people_moderation FROM PUBLIC;
GRANT SELECT ON people_moderation TO taskiem_app;
GRANT UPDATE (seen_at) ON people_moderation TO taskiem_app;
GRANT SELECT (id, person_id, action, reason, operator, report_id, until, lifted_at, lifted_by, lift_reason, closed_at, close_note, seen_at, created_at),
  INSERT, UPDATE ON people_moderation TO taskiem_dispatch;

CREATE TABLE people_appeals (
  id             uuid PRIMARY KEY,
  action_id      uuid NOT NULL UNIQUE REFERENCES people_moderation(id) ON DELETE CASCADE,
  person_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  text           text NOT NULL CHECK (length(text) BETWEEN 1 AND 2000),
  state          text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'upheld', 'overturned')),
  decided_by     text,
  decided_at     timestamptz,
  decision_note  text CHECK (decision_note IS NULL OR length(decision_note) <= 1000),
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX people_appeals_open ON people_appeals (created_at) WHERE state = 'open';
ALTER TABLE people_appeals ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_appeals FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON people_appeals TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope() AND state = 'open'
    AND EXISTS (SELECT 1 FROM people_moderation m WHERE m.id = action_id AND m.person_id = taskiem_person_scope()));
CREATE POLICY dispatch_all ON people_appeals TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON people_appeals FROM PUBLIC;
GRANT SELECT, INSERT ON people_appeals TO taskiem_app;
GRANT SELECT (id, action_id, person_id, text, state, decided_by, decided_at, decision_note, created_at), UPDATE ON people_appeals TO taskiem_dispatch;

-- --- lawful requests ---

CREATE TABLE lawful_requests (
  id              uuid PRIMARY KEY,
  authority       text NOT NULL CHECK (length(authority) BETWEEN 1 AND 200),
  reference       text NOT NULL CHECK (length(reference) BETWEEN 1 AND 200),
  legal_basis     text NOT NULL CHECK (length(legal_basis) BETWEEN 1 AND 500),
  person_id       uuid REFERENCES users(id) ON DELETE SET NULL,
  scope_from      timestamptz NOT NULL,
  scope_to        timestamptz NOT NULL,
  -- Only the chats with this one other person, when the request names one.
  scope_with      uuid,
  scope_files     boolean NOT NULL DEFAULT false,
  scope_note      text NOT NULL DEFAULT '' CHECK (length(scope_note) <= 1000),
  received_at     timestamptz NOT NULL,
  recorded_by     text NOT NULL,
  recorded_at     timestamptz NOT NULL DEFAULT now(),
  approved_by     text,
  approved_at     timestamptz,
  state           text NOT NULL DEFAULT 'recorded' CHECK (state IN ('recorded', 'approved', 'answered', 'refused')),
  answered_by     text,
  answered_at     timestamptz,
  messages_given  int,
  refused_reason  text CHECK (refused_reason IS NULL OR length(refused_reason) <= 1000),
  told_at         timestamptz,
  not_told_reason text CHECK (not_told_reason IS NULL OR length(not_told_reason) <= 1000),
  CHECK (scope_to > scope_from),
  CHECK (approved_by IS NULL OR approved_by <> recorded_by)
);
CREATE INDEX lawful_requests_received ON lawful_requests (received_at);
ALTER TABLE lawful_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE lawful_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY no_app ON lawful_requests TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch_all ON lawful_requests TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON lawful_requests FROM PUBLIC;
GRANT SELECT (id, authority, reference, legal_basis, person_id, scope_from, scope_to, scope_with, scope_files, scope_note, received_at, recorded_by,
  recorded_at, approved_by, approved_at, state, answered_by, answered_at, messages_given, refused_reason, told_at, not_told_reason),
  INSERT, UPDATE ON lawful_requests TO taskiem_dispatch;

-- --- the moderators' functions ---

-- +goose StatementBegin
CREATE FUNCTION taskiem_moderator_check() RETURNS void
LANGUAGE plpgsql STABLE AS $$
BEGIN
  IF NOT taskiem_moderator_scope() THEN
    RAISE EXCEPTION 'only Taskiem''s moderators, in the operator console' USING ERRCODE = '42501';
  END IF;
END
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION taskiem_moderator_check() TO taskiem_app, taskiem_dispatch;

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
         (SELECT count(DISTINCT COALESCE(q.person_id, q.id)) FROM people_reports q WHERE q.reported_id = r.reported_id)
    FROM people_reports r LEFT JOIN person_profiles p ON p.person_id = r.reported_id
   WHERE r.status = p_status OR p_status = 'all'
   ORDER BY r.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

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

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_report_files(p_id uuid)
RETURNS TABLE (message_id uuid, type text, size bigint)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY SELECT f.message_id, f.type, f.size FROM people_report_files f WHERE f.report_id = p_id ORDER BY f.created_at, f.message_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_report_file(p_id uuid, p_message uuid)
RETURNS TABLE (type text, sealed bytea, box_key text, kms_key text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY SELECT f.type, f.sealed, r.box_key, r.kms_key FROM people_report_files f JOIN people_reports r ON r.id = f.report_id
   WHERE f.report_id = p_id AND f.message_id = p_message;
END
$$;
-- +goose StatementEnd

-- An older report's messages, sealed under a key of its own by the
-- safety job (they were plain jsonb before 00480).
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_report_seal(p_id uuid, p_sealed bytea, p_box text, p_kms text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  UPDATE people_reports SET sealed = p_sealed, box_key = p_box, kms_key = p_kms, messages = '[]'::jsonb WHERE id = p_id AND sealed IS NULL;
  RETURN FOUND;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_reports_unsealed(p_limit int)
RETURNS TABLE (id uuid, messages jsonb)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  RETURN QUERY SELECT r.id, r.messages FROM people_reports r WHERE r.sealed IS NULL ORDER BY r.created_at LIMIT LEAST(p_limit, 500);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_customer_reports(p_status text)
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

-- Marks a report (people or customer) reviewed, by the operator, with a note.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_report_review(p_kind text, p_id uuid, p_by text, p_note text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  IF COALESCE(p_by, '') = '' THEN
    RAISE EXCEPTION 'a moderator is named' USING ERRCODE = '22023';
  END IF;
  IF p_kind = 'people' THEN
    UPDATE people_reports SET status = 'reviewed', reviewed_by = p_by, reviewed_at = now(), review_note = NULLIF(btrim(p_note), '')
     WHERE id = p_id AND status = 'open';
  ELSIF p_kind = 'customer' THEN
    UPDATE customer_reports SET status = 'reviewed', reviewed_by = p_by, reviewed_at = now(), review_note = NULLIF(btrim(p_note), '')
     WHERE id = p_id AND status = 'open';
  ELSE
    RAISE EXCEPTION 'a report is people or customer' USING ERRCODE = '22023';
  END IF;
  IF NOT FOUND THEN
    IF (p_kind = 'people' AND EXISTS (SELECT 1 FROM people_reports WHERE id = p_id))
       OR (p_kind = 'customer' AND EXISTS (SELECT 1 FROM customer_reports WHERE id = p_id)) THEN
      RAISE EXCEPTION 'this report was already reviewed' USING ERRCODE = '55000';
    END IF;
    RAISE EXCEPTION 'no such report' USING ERRCODE = 'P0002';
  END IF;
END
$$;
-- +goose StatementEnd

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

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_held_decide(p_id uuid, p_state text, p_by text, p_note text, p_message uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  IF p_state NOT IN ('released', 'confirmed') OR COALESCE(p_by, '') = '' THEN
    RAISE EXCEPTION 'released or confirmed, by a moderator' USING ERRCODE = '22023';
  END IF;
  UPDATE people_held SET state = p_state, decided_by = p_by, decided_at = now(), decision_note = NULLIF(btrim(p_note), ''), released_message = p_message
   WHERE id = p_id AND state = 'held';
  IF NOT FOUND THEN
    IF EXISTS (SELECT 1 FROM people_held WHERE id = p_id) THEN
      RAISE EXCEPTION 'this message was already decided' USING ERRCODE = '55000';
    END IF;
    RAISE EXCEPTION 'no such held message' USING ERRCODE = 'P0002';
  END IF;
END
$$;
-- +goose StatementEnd

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

-- How many accounts have used the devices this person signed in on (the
-- person included); 1 for a person signed in only on the web.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_device_accounts(p_person uuid) RETURNS int
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT GREATEST(1, (SELECT count(DISTINCT o.user_id)::int FROM sessions o
    WHERE o.device_id IN (SELECT s.device_id FROM sessions s WHERE s.user_id = p_person AND s.device_id IS NOT NULL)))
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_people_device_accounts(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_device_accounts(uuid) TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_person(p_person uuid)
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

-- Recomputes whether a person is suspended from what is in force: a
-- suspension or closure neither lifted nor ended (a closure stays in
-- force until the account is closed).
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_standing_sync(p_person uuid) RETURNS boolean
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
REVOKE EXECUTE ON FUNCTION taskiem_people_standing_sync(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_standing_sync(uuid) TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_moderate(p_person uuid, p_action text, p_reason text, p_by text, p_report uuid, p_until timestamptz) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id uuid := gen_random_uuid();
BEGIN
  PERFORM taskiem_moderator_check();
  IF COALESCE(p_by, '') = '' OR COALESCE(btrim(p_reason), '') = '' THEN
    RAISE EXCEPTION 'a moderator and a reason are needed' USING ERRCODE = '22023';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = p_person AND NOT p.deleted) THEN
    RAISE EXCEPTION 'no such account' USING ERRCODE = 'P0002';
  END IF;
  IF p_action IN ('suspend', 'close') AND EXISTS (SELECT 1 FROM people_moderation m WHERE m.person_id = p_person AND m.action = 'close'
       AND m.lifted_at IS NULL AND m.closed_at IS NULL) THEN
    RAISE EXCEPTION 'this account is already being closed' USING ERRCODE = '55000';
  END IF;
  INSERT INTO people_moderation (id, person_id, action, reason, operator, report_id, until)
  VALUES (v_id, p_person, p_action, btrim(p_reason), p_by, p_report, p_until);
  PERFORM taskiem_people_standing_sync(p_person);
  RETURN v_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_lift(p_action uuid, p_by text, p_reason text) RETURNS uuid
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

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_actions(p_person uuid)
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
   WHERE m.person_id = p_person ORDER BY m.created_at DESC LIMIT 200;
END
$$;
-- +goose StatementEnd

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

-- Upholds an appeal (the action stays), or overturns it (the action is
-- lifted). Answers the person.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_people_appeal_decide(p_id uuid, p_state text, p_by text, p_note text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_person uuid;
  v_action uuid;
BEGIN
  PERFORM taskiem_moderator_check();
  IF p_state NOT IN ('upheld', 'overturned') OR COALESCE(p_by, '') = '' THEN
    RAISE EXCEPTION 'upheld or overturned, by a moderator' USING ERRCODE = '22023';
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

-- The safety job: suspensions that ended put the person back; closures
-- whose day came with no open appeal are answered for the server to
-- close the account (taskiem_people_closure_done records it).
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_standing_tick()
RETURNS TABLE (action_id uuid, person_id uuid)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r record;
BEGIN
  PERFORM taskiem_moderator_check();
  FOR r IN SELECT DISTINCT p.person_id FROM person_profiles p WHERE p.suspended LOOP
    PERFORM taskiem_people_standing_sync(r.person_id);
  END LOOP;
  RETURN QUERY
  SELECT m.id, m.person_id FROM people_moderation m
   WHERE m.action = 'close' AND m.lifted_at IS NULL AND m.closed_at IS NULL AND m.until <= now()
     AND NOT EXISTS (SELECT 1 FROM people_appeals a WHERE a.action_id = m.id AND a.state = 'open')
   ORDER BY m.until LIMIT 50;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_people_closure_done(p_action uuid, p_note text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM taskiem_moderator_check();
  UPDATE people_moderation SET closed_at = now(), close_note = p_note WHERE id = p_action AND action = 'close' AND closed_at IS NULL;
END
$$;
-- +goose StatementEnd

-- Retention (people-chatting §14): reviewed reports a year after review
-- (with their files), held messages 90 days after they were held or
-- decided. Answers what went.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_safety_purge(p_reports interval, p_held interval)
RETURNS TABLE (what text, n bigint)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a bigint;
  b bigint;
  c bigint;
BEGIN
  PERFORM taskiem_moderator_check();
  DELETE FROM people_reports WHERE status = 'reviewed' AND reviewed_at < now() - p_reports;
  GET DIAGNOSTICS a = ROW_COUNT;
  DELETE FROM customer_reports WHERE status = 'reviewed' AND reviewed_at < now() - p_reports;
  GET DIAGNOSTICS b = ROW_COUNT;
  DELETE FROM people_held WHERE COALESCE(decided_at, created_at) < now() - p_held;
  GET DIAGNOSTICS c = ROW_COUNT;
  RETURN QUERY VALUES ('people_reports', a), ('customer_reports', b), ('people_held', c);
END
$$;
-- +goose StatementEnd

-- Deleting an account (its own person, in their transaction): their
-- reports stay, detached from them; held messages they sent that no one
-- decided go (they can no longer be delivered).
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

-- The abuse signals a sender's allowance weighs (§8.2, §8.4): how many
-- people reported them in 30 days, and how many accounts used the devices
-- they signed in on. For the person in scope only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_sender_signals()
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

-- --- lawful requests ---

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

-- A step of a lawful request: approve (a second operator, never the one
-- who recorded it), answered (with how many messages were given), refused
-- (with why), told (when the person was told, or why the law forbids it).
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_lawful_step(p_id uuid, p_step text, p_by text, p_n int, p_text text, p_at timestamptz) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l lawful_requests%ROWTYPE;
BEGIN
  PERFORM taskiem_moderator_check();
  IF COALESCE(p_by, '') = '' THEN
    RAISE EXCEPTION 'an operator is named' USING ERRCODE = '22023';
  END IF;
  SELECT * INTO l FROM lawful_requests WHERE id = p_id FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such request' USING ERRCODE = 'P0002';
  END IF;
  IF p_step = 'approve' THEN
    IF l.state <> 'recorded' THEN
      RAISE EXCEPTION 'only a recorded request is approved' USING ERRCODE = '55000';
    END IF;
    IF l.recorded_by = p_by THEN
      RAISE EXCEPTION 'a second operator approves: not the one who recorded it' USING ERRCODE = '42501';
    END IF;
    UPDATE lawful_requests SET state = 'approved', approved_by = p_by, approved_at = now() WHERE id = p_id;
  ELSIF p_step = 'answered' THEN
    IF l.state NOT IN ('approved', 'answered') THEN
      RAISE EXCEPTION 'a request is answered only once a second operator approved it' USING ERRCODE = '55000';
    END IF;
    UPDATE lawful_requests SET state = 'answered', answered_by = p_by, answered_at = now(), messages_given = p_n WHERE id = p_id;
  ELSIF p_step = 'refuse' THEN
    IF l.state NOT IN ('recorded', 'approved') OR COALESCE(btrim(p_text), '') = '' THEN
      RAISE EXCEPTION 'an unanswered request is refused, with why' USING ERRCODE = '55000';
    END IF;
    UPDATE lawful_requests SET state = 'refused', answered_by = p_by, answered_at = now(), refused_reason = btrim(p_text) WHERE id = p_id;
  ELSIF p_step = 'told' THEN
    IF p_at IS NULL AND COALESCE(btrim(p_text), '') = '' THEN
      RAISE EXCEPTION 'say when the person was told, or why the law forbids telling them' USING ERRCODE = '22023';
    END IF;
    UPDATE lawful_requests SET told_at = p_at, not_told_reason = CASE WHEN p_at IS NULL THEN btrim(p_text) END WHERE id = p_id;
  ELSE
    RAISE EXCEPTION 'no such step' USING ERRCODE = '22023';
  END IF;
END
$$;
-- +goose StatementEnd

-- The published count (§8.5, decision 0069 point 11): requests received,
-- answered and refused, per half-year by when they were received. Numbers
-- only; anyone may read them.
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
                           'taskiem_people_closure_done(uuid, text)',
                           'taskiem_people_safety_purge(interval, interval)',
                           'taskiem_people_reports_detach(uuid)',
                           'taskiem_people_sender_signals()',
                           'taskiem_ops_lawful_record(uuid, text, text, text, uuid, timestamptz, timestamptz, uuid, boolean, text, timestamptz, text)',
                           'taskiem_ops_lawful_requests(uuid)',
                           'taskiem_ops_lawful_step(uuid, text, text, int, text, timestamptz)',
                           'taskiem_lawful_request_counts()'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
  FOREACH f IN ARRAY ARRAY['taskiem_people_device_accounts(uuid)', 'taskiem_people_standing_sync(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_lawful_request_counts()',
                           'taskiem_ops_lawful_step(uuid, text, text, int, text, timestamptz)',
                           'taskiem_ops_lawful_requests(uuid)',
                           'taskiem_ops_lawful_record(uuid, text, text, text, uuid, timestamptz, timestamptz, uuid, boolean, text, timestamptz, text)',
                           'taskiem_people_sender_signals()',
                           'taskiem_people_reports_detach(uuid)',
                           'taskiem_people_safety_purge(interval, interval)',
                           'taskiem_people_closure_done(uuid, text)',
                           'taskiem_people_standing_tick()',
                           'taskiem_ops_people_appeal_decide(uuid, text, text, text)',
                           'taskiem_ops_people_appeals(text)',
                           'taskiem_ops_people_actions(uuid)',
                           'taskiem_ops_people_lift(uuid, text, text)',
                           'taskiem_ops_people_moderate(uuid, text, text, text, uuid, timestamptz)',
                           'taskiem_people_standing_sync(uuid)',
                           'taskiem_ops_people_person(uuid)',
                           'taskiem_people_device_accounts(uuid)',
                           'taskiem_ops_people_verdicts(int)',
                           'taskiem_ops_people_held_decide(uuid, text, text, text, uuid)',
                           'taskiem_ops_people_held_one(uuid)',
                           'taskiem_ops_people_held(text)',
                           'taskiem_ops_report_review(text, uuid, text, text)',
                           'taskiem_ops_customer_report(uuid)',
                           'taskiem_ops_customer_reports(text)',
                           'taskiem_ops_people_reports_unsealed(int)',
                           'taskiem_ops_people_report_seal(uuid, bytea, text, text)',
                           'taskiem_ops_people_report_file(uuid, uuid)',
                           'taskiem_ops_people_report_files(uuid)',
                           'taskiem_ops_people_report(uuid)',
                           'taskiem_ops_people_reports(text)',
                           'taskiem_moderator_check()',
                           'taskiem_moderator_scope()'] LOOP
    EXECUTE format('DROP FUNCTION %s', f);
  END LOOP;
END $$;
-- +goose StatementEnd
DROP TABLE lawful_requests;
DROP TABLE people_appeals;
DROP TABLE people_moderation;
-- 00471's body.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_person_profile_sync() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO person_profiles (person_id, name, active, deleted)
  VALUES (NEW.id, COALESCE(NEW.name, ''), NEW.status = 'active', NEW.deleted_at IS NOT NULL)
  ON CONFLICT (person_id) DO UPDATE SET name = EXCLUDED.name, active = EXCLUDED.active, deleted = EXCLUDED.deleted, updated_at = now();
  RETURN NULL;
END
$$;
-- +goose StatementEnd
UPDATE person_profiles p SET active = u.status = 'active' FROM users u WHERE u.id = p.person_id AND p.suspended;
ALTER TABLE person_profiles DROP COLUMN suspended;
REVOKE SELECT (verdict, reasons, at) ON people_screen_verdicts FROM taskiem_dispatch;
DROP TABLE people_held;
DROP TABLE people_report_files;
DROP POLICY dispatch_all ON customer_reports;
REVOKE ALL ON customer_reports FROM taskiem_dispatch;
DROP INDEX customer_reports_reviewed;
DELETE FROM customer_reports WHERE person_id IS NULL;
ALTER TABLE customer_reports
  DROP COLUMN review_note, DROP COLUMN reviewed_at, DROP COLUMN reviewed_by, DROP COLUMN detached_at,
  DROP CONSTRAINT customer_reports_person_id_fkey,
  ADD CONSTRAINT customer_reports_person_id_fkey FOREIGN KEY (person_id) REFERENCES users(id) ON DELETE CASCADE,
  ALTER COLUMN person_id SET NOT NULL;
DROP POLICY dispatch_all ON people_reports;
REVOKE ALL ON people_reports FROM taskiem_dispatch;
DROP INDEX people_reports_reviewed;
DROP INDEX people_reports_reported;
-- A sealed report cannot be opened in SQL: it goes down with its key.
DELETE FROM people_reports WHERE person_id IS NULL OR sealed IS NOT NULL;
ALTER TABLE people_reports
  DROP CONSTRAINT people_reports_box,
  DROP COLUMN review_note, DROP COLUMN reviewed_at, DROP COLUMN reviewed_by, DROP COLUMN detached_at, DROP COLUMN files,
  DROP COLUMN kms_key, DROP COLUMN box_key, DROP COLUMN sealed,
  DROP CONSTRAINT people_reports_person_id_fkey,
  ADD CONSTRAINT people_reports_person_id_fkey FOREIGN KEY (person_id) REFERENCES users(id) ON DELETE CASCADE,
  ALTER COLUMN person_id SET NOT NULL;
