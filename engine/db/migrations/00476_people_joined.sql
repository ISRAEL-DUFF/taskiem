-- People chatting with people, stage 5a: the two streams joined
-- (docs/spec/people-chatting.md, "As built: stage 5a, the two streams
-- joined"; decision 0069).
--
-- * One table of the names people are shown by. Stream A (00471) kept
--   person_profiles (name, active, deleted) and stream B (00475)
--   person_names (name), each a copy of users kept by a trigger, so
--   taskiem_dispatch never reads users.name. person_profiles serves both:
--   finding people's definers (taskiem_people_cards,
--   taskiem_person_link_find) now read it, and person_names, its trigger
--   and its function go. person_profiles takes person_names' narrower
--   grant: taskiem_dispatch updates only the columns the trigger writes.
-- * taskiem_people_between: for the person in scope and others, whether
--   either blocked the other (stream A's people_blocks) and whether they
--   have a people chat that is no longer a request (an accepted copy on
--   either side), which finding people asks (People.Blocked,
--   People.Chatting): to hide a person across a block, and to show a
--   number to the people its owner chats with. Nothing else of the other.
-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_cards(p_viewer uuid, p_owners uuid[])
RETURNS TABLE (person_id uuid, active boolean, name text, owner_has_viewer boolean, viewer_has_owner boolean, viewer_named text,
               message text, photo_at timestamptz, last_seen_at timestamptz, online boolean, read_ticks boolean, number text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_scope uuid := taskiem_person_scope();
BEGIN
  IF p_viewer IS NULL OR v_scope IS NULL
     OR NOT (p_viewer = v_scope OR (cardinality(p_owners) = 1 AND p_owners[1] = v_scope)) THEN
    RAISE EXCEPTION 'people are seen by a person in scope' USING ERRCODE = '42501';
  END IF;
  IF cardinality(p_owners) > 500 THEN
    RAISE EXCEPTION 'at most 500 people at once' USING ERRCODE = '22023';
  END IF;
  RETURN QUERY
  WITH o AS (SELECT DISTINCT x AS id FROM unnest(p_owners) x WHERE x IS NOT NULL),
  v AS (SELECT COALESCE(vp.last_seen, 'contacts') AS last_seen, COALESCE(vp.read_ticks, true) AS read_ticks
          FROM (SELECT 1) one LEFT JOIN person_privacy vp ON vp.person_id = p_viewer),
  c AS (SELECT u.id, (u.status = 'active' AND u.deleted_at IS NULL) AS live, COALESCE(pn.name, '') AS name,
               s.find_by_number, s.message, s.last_seen, s.read_ticks, s.photo, s.show_number
          FROM o JOIN users u ON u.id = o.id LEFT JOIN person_profiles pn ON pn.person_id = u.id
          LEFT JOIN person_privacy s ON s.person_id = u.id)
  SELECT c.id, c.live,
         CASE WHEN c.live THEN c.name ELSE '' END,
         EXISTS (SELECT 1 FROM person_contacts k WHERE k.person_id = c.id AND k.contact_id = p_viewer),
         EXISTS (SELECT 1 FROM person_contacts k WHERE k.person_id = p_viewer AND k.contact_id = c.id),
         CASE WHEN p_viewer = v_scope THEN (SELECT k.name FROM person_contacts k WHERE k.person_id = p_viewer AND k.contact_id = c.id) END,
         COALESCE(c.message, 'everyone'),
         CASE WHEN c.live AND taskiem_people_allows(COALESCE(c.photo, 'contacts'), c.id, p_viewer)
              THEN (SELECT ph.updated_at FROM person_photos ph WHERE ph.person_id = c.id) END,
         CASE WHEN c.live AND taskiem_people_allows(COALESCE(c.last_seen, 'contacts'), c.id, p_viewer)
                   AND taskiem_people_allows(v.last_seen, p_viewer, c.id)
              THEN (SELECT pr.last_seen_at FROM person_presence pr WHERE pr.person_id = c.id) END,
         CASE WHEN c.live AND taskiem_people_allows(COALESCE(c.last_seen, 'contacts'), c.id, p_viewer)
                   AND taskiem_people_allows(v.last_seen, p_viewer, c.id)
              THEN COALESCE((SELECT pr.online_until > now() FROM person_presence pr WHERE pr.person_id = c.id), false) END,
         c.live AND COALESCE(c.read_ticks, true) AND v.read_ticks,
         CASE WHEN c.live AND COALESCE(c.show_number, false) THEN (SELECT p.number FROM user_phones p WHERE p.user_id = c.id) END
    FROM c CROSS JOIN v;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_person_link_find(p_hash bytea)
RETURNS TABLE (person_id uuid, replaced_at timestamptz, name text, photo_public boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.person_id, l.replaced_at, COALESCE(pn.name, ''),
         COALESCE(s.photo, 'contacts') = 'everyone' AND EXISTS (SELECT 1 FROM person_photos ph WHERE ph.person_id = l.person_id)
    FROM person_links l
    JOIN users u ON u.id = l.person_id AND u.status = 'active' AND u.deleted_at IS NULL
    LEFT JOIN person_profiles pn ON pn.person_id = l.person_id
    LEFT JOIN person_privacy s ON s.person_id = l.person_id
   WHERE l.code_hash = p_hash
$$;
-- +goose StatementEnd

DROP TRIGGER person_name_sync ON users;
DROP FUNCTION taskiem_person_name_sync();
DROP TABLE person_names;

REVOKE UPDATE ON person_profiles FROM taskiem_dispatch;
GRANT UPDATE (name, active, deleted, updated_at) ON person_profiles TO taskiem_dispatch;

-- What taskiem_people_between reads: who blocked whom, and whether a copy
-- was accepted.
CREATE POLICY dispatch_read ON people_blocks FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (person_id, blocked_id) ON people_blocks TO taskiem_dispatch;
GRANT SELECT (accepted_at) ON people_chats TO taskiem_dispatch;

-- For the person in scope (no business in scope) and up to 500 others:
-- whether either blocked the other, and whether a people chat between
-- them is no longer a request (either copy accepted).
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_between(p_others uuid[])
RETURNS TABLE (person_id uuid, blocked boolean, chatting boolean)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  me uuid := taskiem_person_scope();
BEGIN
  IF me IS NULL OR taskiem_person_business() IS NOT NULL THEN
    RAISE EXCEPTION 'for a person''s own transaction' USING ERRCODE = '42501';
  END IF;
  IF cardinality(p_others) > 500 THEN
    RAISE EXCEPTION 'at most 500 people at once' USING ERRCODE = '22023';
  END IF;
  RETURN QUERY
  SELECT o.id,
         EXISTS (SELECT 1 FROM people_blocks b WHERE (b.person_id = me AND b.blocked_id = o.id) OR (b.person_id = o.id AND b.blocked_id = me)),
         EXISTS (SELECT 1 FROM people_chats c WHERE ((c.person_id = me AND c.peer_id = o.id) OR (c.person_id = o.id AND c.peer_id = me))
                   AND c.accepted_at IS NOT NULL)
    FROM (SELECT DISTINCT x AS id FROM unnest(p_others) x WHERE x IS NOT NULL AND x <> me) o;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_people_between(uuid[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_people_between(uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_between(uuid[]) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_people_between(uuid[]);
REVOKE SELECT (accepted_at) ON people_chats FROM taskiem_dispatch;
REVOKE SELECT (person_id, blocked_id) ON people_blocks FROM taskiem_dispatch;
DROP POLICY dispatch_read ON people_blocks;

REVOKE UPDATE (name, active, deleted, updated_at) ON person_profiles FROM taskiem_dispatch;
GRANT UPDATE ON person_profiles TO taskiem_dispatch;

CREATE TABLE person_names (
  person_id  uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  name       text NOT NULL
);
ALTER TABLE person_names ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_names FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON person_names TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON person_names TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON person_names FROM PUBLIC;
GRANT SELECT (person_id, name), INSERT, UPDATE (name) ON person_names TO taskiem_dispatch;
INSERT INTO person_names (person_id, name) SELECT id, name FROM users;

-- +goose StatementBegin
CREATE FUNCTION taskiem_person_name_sync() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO person_names (person_id, name) VALUES (NEW.id, NEW.name)
  ON CONFLICT (person_id) DO UPDATE SET name = EXCLUDED.name;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_person_name_sync() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_person_name_sync() FROM PUBLIC;
CREATE TRIGGER person_name_sync AFTER INSERT OR UPDATE OF name ON users
  FOR EACH ROW EXECUTE FUNCTION taskiem_person_name_sync();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_people_cards(p_viewer uuid, p_owners uuid[])
RETURNS TABLE (person_id uuid, active boolean, name text, owner_has_viewer boolean, viewer_has_owner boolean, viewer_named text,
               message text, photo_at timestamptz, last_seen_at timestamptz, online boolean, read_ticks boolean, number text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_scope uuid := taskiem_person_scope();
BEGIN
  IF p_viewer IS NULL OR v_scope IS NULL
     OR NOT (p_viewer = v_scope OR (cardinality(p_owners) = 1 AND p_owners[1] = v_scope)) THEN
    RAISE EXCEPTION 'people are seen by a person in scope' USING ERRCODE = '42501';
  END IF;
  IF cardinality(p_owners) > 500 THEN
    RAISE EXCEPTION 'at most 500 people at once' USING ERRCODE = '22023';
  END IF;
  RETURN QUERY
  WITH o AS (SELECT DISTINCT x AS id FROM unnest(p_owners) x WHERE x IS NOT NULL),
  v AS (SELECT COALESCE(vp.last_seen, 'contacts') AS last_seen, COALESCE(vp.read_ticks, true) AS read_ticks
          FROM (SELECT 1) one LEFT JOIN person_privacy vp ON vp.person_id = p_viewer),
  c AS (SELECT u.id, (u.status = 'active' AND u.deleted_at IS NULL) AS live, COALESCE(pn.name, '') AS name,
               s.find_by_number, s.message, s.last_seen, s.read_ticks, s.photo, s.show_number
          FROM o JOIN users u ON u.id = o.id LEFT JOIN person_names pn ON pn.person_id = u.id
          LEFT JOIN person_privacy s ON s.person_id = u.id)
  SELECT c.id, c.live,
         CASE WHEN c.live THEN c.name ELSE '' END,
         EXISTS (SELECT 1 FROM person_contacts k WHERE k.person_id = c.id AND k.contact_id = p_viewer),
         EXISTS (SELECT 1 FROM person_contacts k WHERE k.person_id = p_viewer AND k.contact_id = c.id),
         CASE WHEN p_viewer = v_scope THEN (SELECT k.name FROM person_contacts k WHERE k.person_id = p_viewer AND k.contact_id = c.id) END,
         COALESCE(c.message, 'everyone'),
         CASE WHEN c.live AND taskiem_people_allows(COALESCE(c.photo, 'contacts'), c.id, p_viewer)
              THEN (SELECT ph.updated_at FROM person_photos ph WHERE ph.person_id = c.id) END,
         CASE WHEN c.live AND taskiem_people_allows(COALESCE(c.last_seen, 'contacts'), c.id, p_viewer)
                   AND taskiem_people_allows(v.last_seen, p_viewer, c.id)
              THEN (SELECT pr.last_seen_at FROM person_presence pr WHERE pr.person_id = c.id) END,
         CASE WHEN c.live AND taskiem_people_allows(COALESCE(c.last_seen, 'contacts'), c.id, p_viewer)
                   AND taskiem_people_allows(v.last_seen, p_viewer, c.id)
              THEN COALESCE((SELECT pr.online_until > now() FROM person_presence pr WHERE pr.person_id = c.id), false) END,
         c.live AND COALESCE(c.read_ticks, true) AND v.read_ticks,
         CASE WHEN c.live AND COALESCE(c.show_number, false) THEN (SELECT p.number FROM user_phones p WHERE p.user_id = c.id) END
    FROM c CROSS JOIN v;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_person_link_find(p_hash bytea)
RETURNS TABLE (person_id uuid, replaced_at timestamptz, name text, photo_public boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.person_id, l.replaced_at, COALESCE(pn.name, ''),
         COALESCE(s.photo, 'contacts') = 'everyone' AND EXISTS (SELECT 1 FROM person_photos ph WHERE ph.person_id = l.person_id)
    FROM person_links l
    JOIN users u ON u.id = l.person_id AND u.status = 'active' AND u.deleted_at IS NULL
    LEFT JOIN person_names pn ON pn.person_id = l.person_id
    LEFT JOIN person_privacy s ON s.person_id = l.person_id
   WHERE l.code_hash = p_hash
$$;
-- +goose StatementEnd
