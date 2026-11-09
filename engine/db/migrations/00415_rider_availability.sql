-- Riders as participants, phase 3 (docs/spec/chat-riders.md, decision
-- 0065): a rider's business says whether it is free, busy till a time or
-- off, and the businesses that use it read that as they read a rider's
-- words today (rider_presence, 00242; held deliveries, 00243). No second
-- store: the rider's business writes the row of the rider it is, through
-- the riders rows linked to it (riders.link_id, 00413).
-- +goose Up
-- The definers (owned by taskiem_dispatch) read which link a rider is.
GRANT SELECT (link_id) ON riders TO taskiem_dispatch;

-- What the rider rows linked to the business in scope are: their phone
-- hashes (one rider, one hash, however many businesses added them).
-- +goose StatementBegin
CREATE FUNCTION taskiem_rider_own_hashes(p_business uuid) RETURNS SETOF bytea
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT r.phone_hash
    FROM riders r JOIN business_links l ON l.id = r.link_id AND l.status = 'active'
    JOIN tenants t ON t.id = r.tenant_id AND t.status = 'active'
   WHERE r.retired_at IS NULL AND r.tenant_id <> p_business AND (l.a_tenant = p_business OR l.b_tenant = p_business)
$$;
-- +goose StatementEnd

-- The rider's business says on, off, or busy until a time, for every
-- business that uses it. Answers how many businesses that is; 0 when no
-- business has linked with it as a rider yet (nothing written).
-- +goose StatementBegin
CREATE FUNCTION taskiem_rider_availability_say(p_said text, p_until timestamptz) RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  n int;
BEGIN
  IF p_said NOT IN ('on', 'off', 'busy') OR (p_said = 'busy' AND p_until IS NULL) THEN
    RAISE EXCEPTION 'on, off, or busy with a time' USING ERRCODE = '22023';
  END IF;
  SELECT count(DISTINCT r.tenant_id) INTO n FROM riders r
   WHERE r.retired_at IS NULL AND r.phone_hash IN (SELECT taskiem_rider_own_hashes(v_me));
  IF n = 0 THEN
    RETURN 0;
  END IF;
  IF p_said = 'on' THEN
    DELETE FROM rider_presence p WHERE p.phone_hash IN (SELECT taskiem_rider_own_hashes(v_me));
  ELSE
    INSERT INTO rider_presence (phone_hash, said, until)
    SELECT h, p_said, CASE WHEN p_said = 'busy' THEN p_until END FROM taskiem_rider_own_hashes(v_me) h
    ON CONFLICT (phone_hash) DO UPDATE SET said = EXCLUDED.said, until = EXCLUDED.until, said_at = now();
  END IF;
  RETURN n;
END
$$;
-- +goose StatementEnd

-- The rider's business reads its own state: what it said (while it
-- holds), the deliveries it holds now (busy), and how many businesses
-- use it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_rider_availability()
RETURNS TABLE (said text, until timestamptz, said_at timestamptz, open int, open_since timestamptz, businesses int)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH mine AS (SELECT taskiem_rider_own_hashes(taskiem_link_self()) AS h),
  said AS (
    SELECT p.said, p.until, p.said_at FROM rider_presence p
     WHERE p.phone_hash IN (SELECT h FROM mine) AND (p.said = 'off' OR p.until > now())
     ORDER BY p.said_at DESC LIMIT 1
  ),
  held AS (
    SELECT count(*)::int AS n, min(d.accepted_at) AS since
      FROM deliveries d JOIN riders r ON r.id = d.rider_id
     WHERE r.phone_hash IN (SELECT h FROM mine) AND d.status IN ('accepted', 'picked_up', 'arrived')
  ),
  uses AS (SELECT count(DISTINCT r.tenant_id)::int AS n FROM riders r WHERE r.retired_at IS NULL AND r.phone_hash IN (SELECT h FROM mine))
  SELECT s.said, s.until, s.said_at, held.n, held.since, uses.n
    FROM held, uses LEFT JOIN said s ON true
$$;
-- +goose StatementEnd

-- What Explore may show of a business that delivers: free, busy or off,
-- NULL when no business has linked with it as a rider (nothing to know),
-- and only for a business that chose to be found or one we are linked
-- with. Never a business, an order or a time.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_availability(p_business uuid) RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH ok AS (
    SELECT 1 WHERE p_business <> taskiem_link_self() AND (
      taskiem_link_findable(p_business)
      OR EXISTS (SELECT 1 FROM business_links l WHERE l.status = 'active'
                  AND l.a_tenant = LEAST(taskiem_link_self(), p_business) AND l.b_tenant = GREATEST(taskiem_link_self(), p_business)))
  ), theirs AS (SELECT taskiem_rider_own_hashes(p_business) AS h FROM ok)
  SELECT CASE
    WHEN NOT EXISTS (SELECT 1 FROM theirs) THEN NULL
    WHEN EXISTS (SELECT 1 FROM rider_presence p WHERE p.phone_hash IN (SELECT h FROM theirs) AND p.said = 'off') THEN 'off'
    WHEN EXISTS (SELECT 1 FROM rider_presence p WHERE p.phone_hash IN (SELECT h FROM theirs) AND p.said = 'busy' AND p.until > now()) THEN 'busy'
    WHEN EXISTS (SELECT 1 FROM deliveries d JOIN riders r ON r.id = d.rider_id WHERE r.phone_hash IN (SELECT h FROM theirs) AND d.status IN ('accepted', 'picked_up', 'arrived')) THEN 'busy'
    ELSE 'free' END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_rider_own_hashes(uuid)', 'taskiem_rider_availability_say(text, timestamptz)', 'taskiem_rider_availability()', 'taskiem_link_availability(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
  END LOOP;
  FOREACH f IN ARRAY ARRAY['taskiem_rider_availability_say(text, timestamptz)', 'taskiem_rider_availability()', 'taskiem_link_availability(uuid)'] LOOP
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
REVOKE SELECT (link_id) ON riders FROM taskiem_dispatch;
DROP FUNCTION taskiem_link_availability(uuid);
DROP FUNCTION taskiem_rider_availability();
DROP FUNCTION taskiem_rider_availability_say(text, timestamptz);
DROP FUNCTION taskiem_rider_own_hashes(uuid);
