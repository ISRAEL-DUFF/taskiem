-- Finding people (docs/spec/people-chatting.md §5, §8.4, §14; decision
-- 0069; stage 5a, stream B): a person's privacy settings towards other
-- people, their contacts on Taskiem, their link, when they were last seen,
-- and the verdicts of the automated checks on strangers' messages.
--
-- * person_privacy: the settings of §5.4 (no row: the defaults). Who may
--   find me by my number (everyone, contacts, nobody; contacts), find me
--   from their phone's contacts (on), message me (everyone as a request,
--   contacts, nobody; everyone), see my last seen and online (contacts),
--   my read ticks (on; off means not seeing others' either), my photo
--   (contacts), and whether my number is shown to people I chat with
--   (off).
-- * person_contacts: the people a person chose to add (from a lookup by
--   number, a link, contact matching or a chat), with the name they gave.
--   "My contacts" in a setting is the owner's own list: whom the owner
--   added, not whether they added the owner back.
-- * person_links: a person's link, https://<host>/p/{code}, as a business's
--   chat link (engine/chatlinks): kept only as a keyed hash with the nonce
--   it was made from; replaced at will, the old code answering "no longer
--   works" for 30 days.
-- * person_presence: when the person was last seen, and until when they
--   count as online (the app's heartbeat).
-- * people_find_counts: how many lookups and contact hashes a person sent
--   each day (the limits against enumeration); never what was looked up.
-- * person_names: the name a person is shown to others by, a copy of
--   users.name kept by a trigger, so taskiem_dispatch never reads users.name.
-- * people_screen_verdicts: the verdict of the checks on a stranger's
--   message (warn or hold) and its reasons; never the words.
--
-- The definers each answer for the person in scope only:
-- taskiem_people_cards (what a person may see of others, by their
-- settings), taskiem_people_find_number, taskiem_people_match (contact
-- matching by keyed hashes; nothing of the request is kept),
-- taskiem_people_photo, taskiem_person_link_find (a code's person, for
-- anyone), taskiem_people_forget (on deleting an account).
-- +goose Up

-- What the definers read of an account: whether it was deleted. Its name
-- is not readable by taskiem_dispatch (users.name stays a payload); the
-- name people are shown is kept apart, in person_names, which a trigger
-- keeps in step with users.name.
GRANT SELECT (deleted_at) ON users TO taskiem_dispatch;

-- The name a person is shown to other people by (people-chatting §5):
-- a copy of users.name, written only by the trigger below, read only by
-- the definers here. The application reads and writes users.name as
-- before.
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

CREATE TABLE person_privacy (
  person_id           uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  find_by_number      text NOT NULL DEFAULT 'contacts' CHECK (find_by_number IN ('everyone', 'contacts', 'nobody')),
  find_from_contacts  boolean NOT NULL DEFAULT true,
  message             text NOT NULL DEFAULT 'everyone' CHECK (message IN ('everyone', 'contacts', 'nobody')),
  last_seen           text NOT NULL DEFAULT 'contacts' CHECK (last_seen IN ('everyone', 'contacts', 'nobody')),
  read_ticks          boolean NOT NULL DEFAULT true,
  photo               text NOT NULL DEFAULT 'contacts' CHECK (photo IN ('everyone', 'contacts', 'nobody')),
  show_number         boolean NOT NULL DEFAULT false,
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE person_contacts (
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  contact_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        text CHECK (name IS NULL OR length(name) BETWEEN 1 AND 80),
  source      text NOT NULL CHECK (source IN ('number', 'link', 'contacts', 'chat')),
  added_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (person_id, contact_id),
  CHECK (person_id <> contact_id)
);
CREATE INDEX person_contacts_contact ON person_contacts (contact_id);

CREATE TABLE person_links (
  id           uuid PRIMARY KEY,
  person_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash    bytea NOT NULL UNIQUE CHECK (length(code_hash) = 32),
  nonce        bytea NOT NULL CHECK (length(nonce) = 16),
  created_at   timestamptz NOT NULL DEFAULT now(),
  replaced_at  timestamptz
);
CREATE UNIQUE INDEX person_links_live ON person_links (person_id) WHERE replaced_at IS NULL;

CREATE TABLE person_presence (
  person_id     uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  online_until  timestamptz
);

CREATE TABLE people_find_counts (
  person_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind       text NOT NULL CHECK (kind IN ('number', 'contacts', 'contacts_batch')),
  day        date NOT NULL,
  n          int NOT NULL DEFAULT 0,
  PRIMARY KEY (person_id, kind, day)
);

CREATE TABLE people_screen_verdicts (
  id           uuid PRIMARY KEY,
  from_person  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  to_person    uuid REFERENCES users(id) ON DELETE SET NULL,
  verdict      text NOT NULL CHECK (verdict IN ('warn', 'hold')),
  reasons      text[] NOT NULL DEFAULT '{}',
  at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX people_screen_verdicts_from ON people_screen_verdicts (from_person, at);
CREATE INDEX people_screen_verdicts_to ON people_screen_verdicts (to_person) WHERE to_person IS NOT NULL;

-- The person's own rows, with no door into a business open.
-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['person_privacy', 'person_contacts', 'person_links', 'person_presence', 'people_find_counts'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY person_own ON %I TO taskiem_app
      USING (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
      WITH CHECK (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)', t);
    EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
    EXECUTE format('CREATE POLICY dispatch ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', t);
    EXECUTE format('GRANT DELETE ON %I TO taskiem_dispatch', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- A sender's verdicts: written as the sender, read by them (to count them,
-- never the words, which are not kept); no update.
ALTER TABLE people_screen_verdicts ENABLE ROW LEVEL SECURITY;
ALTER TABLE people_screen_verdicts FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON people_screen_verdicts TO taskiem_app
  USING (from_person = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (from_person = taskiem_person_scope() AND taskiem_person_business() IS NULL);
REVOKE ALL ON people_screen_verdicts FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON people_screen_verdicts TO taskiem_app;
CREATE POLICY dispatch ON people_screen_verdicts TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (from_person, to_person), UPDATE (to_person), DELETE ON people_screen_verdicts TO taskiem_dispatch;

-- What the definers read.
GRANT SELECT (person_id, find_by_number, find_from_contacts, message, last_seen, read_ticks, photo, show_number) ON person_privacy TO taskiem_dispatch;
GRANT SELECT (person_id, contact_id, name) ON person_contacts TO taskiem_dispatch;
GRANT SELECT (person_id, code_hash, replaced_at) ON person_links TO taskiem_dispatch;
GRANT SELECT (person_id, last_seen_at, online_until) ON person_presence TO taskiem_dispatch;
GRANT SELECT (person_id) ON people_find_counts TO taskiem_dispatch;
CREATE POLICY dispatch ON person_photos FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (person_id, type, data, updated_at) ON person_photos TO taskiem_dispatch;

-- Whether a setting's audience lets viewer see owner: everyone; contacts,
-- when the owner added the viewer to their own list; nobody. A person
-- always sees themself; nobody unknown (a NULL viewer, the public) sees
-- more than everyone's. Not callable by the application.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_allows(p_rule text, p_owner uuid, p_viewer uuid)
RETURNS boolean LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT CASE
    WHEN p_viewer IS NOT NULL AND p_viewer = p_owner THEN true
    WHEN p_rule = 'everyone' THEN true
    WHEN p_rule = 'contacts' AND p_viewer IS NOT NULL THEN
      EXISTS (SELECT 1 FROM person_contacts c WHERE c.person_id = p_owner AND c.contact_id = p_viewer)
    ELSE false
  END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_people_allows(text, uuid, uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_people_allows(text, uuid, uuid) FROM PUBLIC;

-- What viewer may see of each of owners (at most 500), by the owners'
-- settings: the name (empty for a deleted account), whether each has the
-- other in their contacts (and the name the viewer gave, for the viewer
-- only), the owner's "who can message me", when the photo was changed
-- (only when the photo setting lets the viewer see it), last seen and
-- online (only when the owner's setting lets the viewer see them and the
-- viewer's own lets the owner see theirs: hidden both ways), whether read
-- ticks go between them (both on), and the owner's number when the owner
-- chose to show it (the caller decides to whom: people they chat with).
-- Called by the viewer in scope, or about one owner by that owner.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_cards(p_viewer uuid, p_owners uuid[])
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

-- The person a typed number names, when their "find me by my number"
-- lets the viewer in scope find them (the viewer's own number names
-- themself); NULL otherwise, so a number that is nobody's and one whose
-- person hides from the viewer answer the same.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_find_number(p_viewer uuid, p_number text)
RETURNS uuid LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id uuid;
BEGIN
  IF p_viewer IS NULL OR p_viewer IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'people are found by a person in scope' USING ERRCODE = '42501';
  END IF;
  SELECT p.user_id INTO v_id
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active' AND u.deleted_at IS NULL
    LEFT JOIN person_privacy s ON s.person_id = p.user_id
   WHERE p.number = p_number
     AND taskiem_people_allows(COALESCE(s.find_by_number, 'contacts'), p.user_id, p_viewer);
  RETURN v_id;
END
$$;
-- +goose StatementEnd

-- Contact matching (§5.3): which of the hashes the viewer in scope sent
-- (sha256(key || 'taskiem-contacts:' || E.164), the key the server gave
-- for this week) are the verified phones of people who let themselves be
-- found from contacts. Nothing is written: the hashes are compared and
-- forgotten. At most 1000 hashes a call; the viewer is never answered.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_match(p_viewer uuid, p_key bytea, p_hashes bytea[])
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

-- A person's photo, when their photo setting lets the viewer in scope see
-- it (a NULL viewer, the public opening a person's link: only
-- everyone's).
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_photo(p_viewer uuid, p_owner uuid)
RETURNS TABLE (type text, data bytea, updated_at timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_viewer IS NOT NULL AND p_viewer IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a photo is seen by a person in scope' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT ph.type, ph.data, ph.updated_at
    FROM person_photos ph
    JOIN users u ON u.id = ph.person_id AND u.status = 'active' AND u.deleted_at IS NULL
    LEFT JOIN person_privacy s ON s.person_id = ph.person_id
   WHERE ph.person_id = p_owner
     AND taskiem_people_allows(COALESCE(s.photo, 'contacts'), p_owner, p_viewer);
END
$$;
-- +goose StatementEnd

-- The person a link's code (its keyed hash) names, when it was replaced,
-- their name and whether their photo is shown to everyone: for anyone, so
-- the public card and /p/{code} can answer. An account that is not active
-- names no one.
-- +goose StatementBegin
CREATE FUNCTION taskiem_person_link_find(p_hash bytea)
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

-- Deleting an account (§14): the person in scope leaves every other
-- person's contacts, and their settings, contacts, link, presence, counts
-- and verdicts go; verdicts about messages to them no longer name them.
-- Answers how many other people's contacts they left.
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_forget(p_person uuid)
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  IF p_person IS NULL OR p_person IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person forgets only themself' USING ERRCODE = '42501';
  END IF;
  DELETE FROM person_contacts WHERE contact_id = p_person;
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM person_contacts WHERE person_id = p_person;
  DELETE FROM person_privacy WHERE person_id = p_person;
  DELETE FROM person_links WHERE person_id = p_person;
  DELETE FROM person_presence WHERE person_id = p_person;
  DELETE FROM people_find_counts WHERE person_id = p_person;
  DELETE FROM people_screen_verdicts WHERE from_person = p_person;
  UPDATE people_screen_verdicts SET to_person = NULL WHERE to_person = p_person;
  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_people_cards(uuid, uuid[])', 'taskiem_people_find_number(uuid, text)',
                           'taskiem_people_match(uuid, bytea, bytea[])', 'taskiem_people_photo(uuid, uuid)',
                           'taskiem_person_link_find(bytea)', 'taskiem_people_forget(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_people_forget(uuid);
DROP FUNCTION taskiem_person_link_find(bytea);
DROP FUNCTION taskiem_people_photo(uuid, uuid);
DROP FUNCTION taskiem_people_match(uuid, bytea, bytea[]);
DROP FUNCTION taskiem_people_find_number(uuid, text);
DROP FUNCTION taskiem_people_cards(uuid, uuid[]);
DROP FUNCTION taskiem_people_allows(text, uuid, uuid);
REVOKE SELECT (person_id, type, data, updated_at) ON person_photos FROM taskiem_dispatch;
DROP POLICY dispatch ON person_photos;
DROP TABLE people_screen_verdicts;
DROP TABLE people_find_counts;
DROP TABLE person_presence;
DROP TABLE person_links;
DROP TABLE person_contacts;
DROP TABLE person_privacy;
DROP TRIGGER person_name_sync ON users;
DROP FUNCTION taskiem_person_name_sync();
DROP TABLE person_names;
REVOKE SELECT (deleted_at) ON users FROM taskiem_dispatch;
