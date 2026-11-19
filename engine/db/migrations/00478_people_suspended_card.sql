-- People chatting with people, stage 5a: a suspended account's card
-- (docs/spec/people-chatting.md, "As built: stage 5a, web", "Web review
-- fixes"; decision 0069).
--
-- * taskiem_people_cards answers deleted apart from active: an account
--   that is not active and not deleted is suspended, and keeps its name
--   (nothing else: no photo, last seen, online, read ticks or number), so
--   GET /v1/people/{id} and the contacts show it as "can't get messages
--   right now", not as "Deleted account". A deleted one is as before.
-- +goose Up

DROP FUNCTION taskiem_people_cards(uuid, uuid[]);
-- +goose StatementBegin
CREATE FUNCTION taskiem_people_cards(p_viewer uuid, p_owners uuid[])
RETURNS TABLE (person_id uuid, active boolean, deleted boolean, name text, owner_has_viewer boolean, viewer_has_owner boolean, viewer_named text,
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
  c AS (SELECT u.id, (u.status = 'active' AND u.deleted_at IS NULL) AS live,
               (u.deleted_at IS NOT NULL OR COALESCE(pn.deleted, false)) AS gone, COALESCE(pn.name, '') AS name,
               s.find_by_number, s.message, s.last_seen, s.read_ticks, s.photo, s.show_number
          FROM o JOIN users u ON u.id = o.id LEFT JOIN person_profiles pn ON pn.person_id = u.id
          LEFT JOIN person_privacy s ON s.person_id = u.id)
  SELECT c.id, c.live, c.gone,
         CASE WHEN NOT c.gone THEN c.name ELSE '' END,
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
ALTER FUNCTION taskiem_people_cards(uuid, uuid[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_people_cards(uuid, uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_cards(uuid, uuid[]) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_people_cards(uuid, uuid[]);
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
ALTER FUNCTION taskiem_people_cards(uuid, uuid[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_people_cards(uuid, uuid[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_people_cards(uuid, uuid[]) TO taskiem_app;
