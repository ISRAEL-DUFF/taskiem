-- Business links: what a request may not reveal (docs/spec/business-links.md
-- §5.1, §13; review of L0).
--
-- A blind request, by phone or address, must tell its sender nothing: not
-- whether a number belongs to a business it already sees (the request now
-- leaves such a row alone), not through the row it leaves behind (a later
-- request by search answers "sent" whether or not a blind one reached the
-- same business), and not through the daily quota (blind requests are
-- counted in the requester's own table only). A business that blocked us
-- cannot be told apart from one that declined: asking either again within
-- the 30 days leaves the clock alone. And a block is each side's own: the
-- side blocked cannot lift it by blocking back, nor by unblocking.
-- +goose Up
ALTER TABLE business_links
  ADD COLUMN a_blocked boolean NOT NULL DEFAULT false,
  ADD COLUMN b_blocked boolean NOT NULL DEFAULT false;
UPDATE business_links SET a_blocked = (blocked_by = a_tenant), b_blocked = (blocked_by = b_tenant) WHERE blocked_by IS NOT NULL;
ALTER TABLE business_links
  ADD CONSTRAINT business_links_blocked_sides CHECK ((status = 'blocked') = (a_blocked OR b_blocked)),
  ADD CONSTRAINT business_links_blocked_by_side CHECK (blocked_by IS NULL OR (blocked_by = a_tenant AND a_blocked) OR (blocked_by = b_tenant AND b_blocked));

-- Whether p_me blocked the link, whichever side it is.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_blocked_by(l business_links, p_me uuid) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE WHEN l.a_tenant = p_me THEN l.a_blocked WHEN l.b_tenant = p_me THEN l.b_blocked ELSE false END
$$;
-- +goose StatementEnd

-- As 00279, with a block read from the caller's own side: a link both
-- sides blocked reads 'blocked' to each.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_view(l business_links, p_me uuid) RETURNS text
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT CASE
    WHEN l.status = 'active' THEN 'active'
    WHEN l.status = 'ended' THEN 'ended'
    WHEN l.status = 'cancelled' THEN NULL
    WHEN l.status = 'blocked' AND taskiem_link_blocked_by(l, p_me) THEN 'blocked'
    -- A link that was made and then blocked reads as ended.
    WHEN l.status = 'blocked' AND l.ended_by IS NOT NULL THEN 'ended'
    WHEN l.requested_by = p_me THEN CASE
      WHEN l.requester_hidden OR l.how = 'blind' THEN NULL
      WHEN l.expires_at > now() THEN 'requested'
      ELSE 'not_accepted' END
    WHEN l.status = 'requested' AND l.expires_at > now() THEN 'asked_us'
    ELSE NULL END
$$;
-- +goose StatementEnd

-- As 00279, and:
--   a blind request to a business whose link the caller already sees
--     (linked, ended, asked either way, or not accepted) changes nothing
--     and tells no one: the caller knows the business, and a blind request
--     that moved the row would confirm the number is its;
--   a request by search or code where a blind one of ours stands, within
--     its 30 days, takes its place (how, and a fresh clock), and no one is
--     told twice: the caller is answered "sent" as for any request;
--   the daily quota counts requests by search and code only; blind ones
--     are counted in the requester's own link_blind_requests, so the count
--     never says whether a blind request reached anyone;
--   asking again a business that blocked us, within our 30 days, leaves
--     the clock as asking one that declined does; the clock restarts only
--     after expiry, as for any other request.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_request(p_target uuid, p_code text, p_phone text, p_slug text, p_role text, p_actor_type text, p_actor_id text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_target uuid := p_target;
  v_how text := 'search';
  v_link business_links%ROWTYPE;
  v_id uuid;
BEGIN
  IF p_role NOT IN ('supplier', 'customer', 'both') THEN
    RAISE EXCEPTION 'a link''s role is supplier, customer or both' USING ERRCODE = '22023';
  END IF;
  IF p_code IS NOT NULL THEN
    v_how := 'code';
    SELECT ls.tenant_id INTO v_target FROM link_settings ls JOIN tenants t ON t.id = ls.tenant_id AND t.status = 'active'
     WHERE ls.code = lower(p_code);
    IF v_target IS NULL OR v_target = v_me THEN
      RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
    END IF;
  ELSIF p_target IS NULL THEN
    v_how := 'blind';
    IF p_phone IS NOT NULL THEN
      v_target := taskiem_link_phone_business(p_phone);
    ELSIF p_slug IS NOT NULL THEN
      SELECT s.tenant_id INTO v_target FROM storefronts s JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
       WHERE s.published_slug = lower(p_slug) AND s.status = 'published';
    END IF;
    IF v_target IS NULL OR v_target = v_me THEN
      RETURN NULL;
    END IF;
  ELSIF v_target = v_me OR NOT taskiem_link_findable(v_target) THEN
    RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
  END IF;
  -- The quota, for requests by search and code: a blind one is counted on
  -- the requester's own side, before this function is called.
  IF v_how <> 'blind' AND (SELECT count(*) FROM business_links WHERE requested_by = v_me AND how <> 'blind' AND requested_at > now() - interval '1 day') >= 20 THEN
    RAISE EXCEPTION 'too many link requests today' USING ERRCODE = '53400';
  END IF;

  SELECT * INTO v_link FROM business_links WHERE a_tenant = LEAST(v_me, v_target) AND b_tenant = GREATEST(v_me, v_target) FOR UPDATE;
  IF FOUND AND v_how = 'blind' AND taskiem_link_view(v_link, v_me) IS NOT NULL THEN
    -- A business the caller already sees: the row is left as it is, so
    -- nothing about it says whether the number or address was its.
    RETURN NULL;
  END IF;
  IF FOUND AND v_link.status IN ('requested', 'declined') AND v_link.expires_at > now() AND v_link.requested_by = v_me THEN
    -- Ours, still within its 30 days (declined or not, as far as we know):
    -- shown again if we had cancelled it, and the other side not told
    -- twice. Asked by search or code where a blind one stood, it is that
    -- request now, with its own clock.
    IF v_link.how = 'blind' AND v_how <> 'blind' THEN
      UPDATE business_links SET how = v_how, role = p_role, requester_actor = p_actor_type || ':' || p_actor_id,
             requested_at = now(), expires_at = now() + interval '30 days', requester_hidden = false
       WHERE id = v_link.id;
      PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, 'link.request', v_link.id::text, jsonb_build_object('how', v_how, 'role', p_role));
    ELSE
      UPDATE business_links SET requester_hidden = false WHERE id = v_link.id;
    END IF;
    RETURN NULL;
  END IF;
  IF FOUND AND (v_link.status = 'active'
      OR (v_link.status = 'requested' AND v_link.expires_at > now() AND v_link.requested_by = v_target)) THEN
    -- Linked, or a request already waiting either way: nothing new.
    RETURN NULL;
  END IF;
  IF FOUND AND v_link.status = 'blocked' AND taskiem_link_blocked_by(v_link, v_target) THEN
    -- Blocked by the business asked: taken and dropped, and read by the
    -- requester exactly as a declined one is. Within our 30 days the clock
    -- stands (a blind one asked by search or code becomes that request, as
    -- above); after them it starts again. Our own block, if we had one
    -- too, is lifted by asking.
    IF v_link.requested_by = v_me AND v_link.expires_at > now() AND v_link.ended_by IS NULL THEN
      IF v_link.how = 'blind' AND v_how <> 'blind' THEN
        UPDATE business_links SET how = v_how, role = p_role, requester_actor = p_actor_type || ':' || p_actor_id,
               requested_at = now(), expires_at = now() + interval '30 days', requester_hidden = false,
               blocked_by = v_target, a_blocked = (a_tenant = v_target), b_blocked = (b_tenant = v_target)
         WHERE id = v_link.id;
        PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, 'link.request', v_link.id::text, jsonb_build_object('how', v_how, 'role', p_role));
      ELSE
        UPDATE business_links SET requester_hidden = false,
               blocked_by = v_target, a_blocked = (a_tenant = v_target), b_blocked = (b_tenant = v_target)
         WHERE id = v_link.id;
      END IF;
      RETURN NULL;
    END IF;
    UPDATE business_links SET requested_by = v_me, requester_actor = p_actor_type || ':' || p_actor_id, how = v_how, role = p_role,
           requested_at = now(), expires_at = now() + interval '30 days', requester_hidden = false, ended_by = NULL, ended_at = NULL,
           blocked_by = v_target, a_blocked = (a_tenant = v_target), b_blocked = (b_tenant = v_target)
     WHERE id = v_link.id;
    PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, 'link.request', v_link.id::text, jsonb_build_object('how', v_how, 'role', p_role));
    RETURN NULL;
  END IF;
  -- Anything else (ended, cancelled, declined or expired, or blocked by
  -- us, which asking again lifts) becomes a new request.
  IF FOUND THEN
    v_id := v_link.id;
    UPDATE business_links SET status = 'requested', requested_by = v_me, requester_actor = p_actor_type || ':' || p_actor_id, how = v_how, role = p_role,
           requested_at = now(), expires_at = now() + interval '30 days', requester_hidden = false,
           answered_at = NULL, answered_actor = NULL, blocked_by = NULL, a_blocked = false, b_blocked = false, ended_by = NULL, ended_at = NULL
     WHERE id = v_id;
  ELSE
    v_id := gen_random_uuid();
    INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at)
    VALUES (v_id, LEAST(v_me, v_target), GREATEST(v_me, v_target), 'requested', v_me, p_actor_type || ':' || p_actor_id, v_how, p_role, now() + interval '30 days');
  END IF;
  PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, 'link.request', v_id::text, jsonb_build_object('how', v_how, 'role', p_role));
  PERFORM taskiem_link_audit_other(v_me, v_target, v_id, p_actor_type, p_actor_id, 'link.requested', jsonb_build_object('how', v_how));
  RETURN v_target;
END
$$;
-- +goose StatementEnd

-- As 00279, with a block kept per side:
--   block: the side asked, or either side of a link (it ends), or a side
--     whose link the other blocked (it reads as ended to it): its own
--     block is recorded beside the other's, and neither learns of the
--     other's;
--   unblock: a side that blocked lifts its own; the link stays blocked
--     while the other side's block stands.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_answer(p_link uuid, p_answer text, p_actor_type text, p_actor_id text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  l business_links%ROWTYPE;
  v_other uuid;
  v_view text;
  v_both boolean := true;
BEGIN
  SELECT * INTO l FROM business_links WHERE id = p_link AND v_me IN (a_tenant, b_tenant) FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such link' USING ERRCODE = 'P0002';
  END IF;
  v_other := CASE WHEN l.a_tenant = v_me THEN l.b_tenant ELSE l.a_tenant END;
  v_view := taskiem_link_view(l, v_me);
  IF p_answer IN ('accept', 'decline') AND v_view = 'asked_us' THEN
    UPDATE business_links SET status = CASE p_answer WHEN 'accept' THEN 'active' ELSE 'declined' END,
           answered_at = now(), answered_actor = p_actor_type || ':' || p_actor_id WHERE id = p_link;
    v_both := p_answer = 'accept';
  ELSIF p_answer = 'block' AND v_view IN ('asked_us', 'active', 'ended') THEN
    -- Our side's block. The other side's, if it blocked first, stands
    -- untouched, and blocked_by stays its.
    UPDATE business_links SET status = 'blocked', blocked_by = COALESCE(blocked_by, v_me),
           a_blocked = a_blocked OR a_tenant = v_me, b_blocked = b_blocked OR b_tenant = v_me,
           answered_at = COALESCE(answered_at, now()),
           ended_by = CASE WHEN l.status = 'active' THEN v_me ELSE ended_by END,
           ended_at = CASE WHEN l.status = 'active' THEN now() ELSE ended_at END WHERE id = p_link;
    -- Ending an active link is told; a block is not.
    v_both := l.status = 'active';
  ELSIF p_answer = 'unblock' AND v_view = 'blocked' THEN
    -- Our own block lifted. While the other side's stands, the link stays
    -- blocked by it. Otherwise a link that was made goes back to ended,
    -- and a request blocked becomes declined: to its requester it still
    -- waits out its 30 days, as it did while blocked, so the unblock is
    -- not told either.
    IF taskiem_link_blocked_by(l, v_other) THEN
      UPDATE business_links SET blocked_by = v_other, a_blocked = a_tenant = v_other, b_blocked = b_tenant = v_other WHERE id = p_link;
    ELSE
      UPDATE business_links SET status = CASE WHEN ended_by IS NOT NULL THEN 'ended' ELSE 'declined' END,
             blocked_by = NULL, a_blocked = false, b_blocked = false WHERE id = p_link;
    END IF;
    v_both := false;
  ELSIF p_answer = 'cancel' AND v_view IN ('requested', 'not_accepted') THEN
    IF l.status = 'requested' THEN
      UPDATE business_links SET status = 'cancelled' WHERE id = p_link;
      v_both := l.expires_at > now();
    ELSE
      UPDATE business_links SET requester_hidden = true WHERE id = p_link;
      v_both := false;
    END IF;
  ELSIF p_answer = 'end' AND v_view = 'active' THEN
    UPDATE business_links SET status = 'ended', ended_by = v_me, ended_at = now() WHERE id = p_link;
  ELSE
    RAISE EXCEPTION 'the link cannot be answered that way now' USING ERRCODE = '55000';
  END IF;
  PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, 'link.' || p_answer, p_link::text, jsonb_build_object('business', v_other));
  IF v_both THEN
    PERFORM taskiem_link_audit_other(v_me, v_other, p_link, p_actor_type, p_actor_id, 'link.' || p_answer, '{}');
  END IF;
  RETURN v_other;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_link_blocked_by(business_links, uuid)', 'taskiem_link_view(business_links, uuid)',
      'taskiem_link_request(uuid, text, text, text, text, text, text)', 'taskiem_link_answer(uuid, text, text, text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
  END LOOP;
  -- What the application calls; the helpers stay the definers' own.
  FOREACH f IN ARRAY ARRAY['taskiem_link_request(uuid, text, text, text, text, text, text)', 'taskiem_link_answer(uuid, text, text, text)'] LOOP
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_view(l business_links, p_me uuid) RETURNS text
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT CASE
    WHEN l.status = 'active' THEN 'active'
    WHEN l.status = 'ended' THEN 'ended'
    WHEN l.status = 'cancelled' THEN NULL
    WHEN l.status = 'blocked' AND l.blocked_by = p_me THEN 'blocked'
    WHEN l.status = 'blocked' AND l.ended_by IS NOT NULL THEN 'ended'
    WHEN l.requested_by = p_me THEN CASE
      WHEN l.requester_hidden OR l.how = 'blind' THEN NULL
      WHEN l.expires_at > now() THEN 'requested'
      ELSE 'not_accepted' END
    WHEN l.status = 'requested' AND l.expires_at > now() THEN 'asked_us'
    ELSE NULL END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_request(p_target uuid, p_code text, p_phone text, p_slug text, p_role text, p_actor_type text, p_actor_id text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_target uuid := p_target;
  v_how text := 'search';
  v_link business_links%ROWTYPE;
  v_id uuid;
BEGIN
  IF p_role NOT IN ('supplier', 'customer', 'both') THEN
    RAISE EXCEPTION 'a link''s role is supplier, customer or both' USING ERRCODE = '22023';
  END IF;
  IF (SELECT count(*) FROM business_links WHERE requested_by = v_me AND requested_at > now() - interval '1 day') >= 20 THEN
    RAISE EXCEPTION 'too many link requests today' USING ERRCODE = '53400';
  END IF;
  IF p_code IS NOT NULL THEN
    v_how := 'code';
    SELECT ls.tenant_id INTO v_target FROM link_settings ls JOIN tenants t ON t.id = ls.tenant_id AND t.status = 'active'
     WHERE ls.code = lower(p_code);
    IF v_target IS NULL OR v_target = v_me THEN
      RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
    END IF;
  ELSIF p_target IS NULL THEN
    v_how := 'blind';
    IF p_phone IS NOT NULL THEN
      v_target := taskiem_link_phone_business(p_phone);
    ELSIF p_slug IS NOT NULL THEN
      SELECT s.tenant_id INTO v_target FROM storefronts s JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
       WHERE s.published_slug = lower(p_slug) AND s.status = 'published';
    END IF;
    IF v_target IS NULL OR v_target = v_me THEN
      RETURN NULL;
    END IF;
  ELSIF v_target = v_me OR NOT taskiem_link_findable(v_target) THEN
    RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
  END IF;

  SELECT * INTO v_link FROM business_links WHERE a_tenant = LEAST(v_me, v_target) AND b_tenant = GREATEST(v_me, v_target) FOR UPDATE;
  IF FOUND AND v_link.status IN ('requested', 'declined') AND v_link.expires_at > now() AND v_link.requested_by = v_me THEN
    UPDATE business_links SET requester_hidden = false WHERE id = v_link.id;
    RETURN NULL;
  END IF;
  IF FOUND AND (v_link.status = 'active'
      OR (v_link.status = 'requested' AND v_link.expires_at > now() AND v_link.requested_by = v_target)) THEN
    RETURN NULL;
  END IF;
  IF FOUND AND v_link.status = 'blocked' AND v_link.blocked_by = v_target THEN
    UPDATE business_links SET requested_by = v_me, requester_actor = p_actor_type || ':' || p_actor_id, how = v_how, role = p_role,
           requested_at = now(), expires_at = now() + interval '30 days', requester_hidden = false, ended_by = NULL, ended_at = NULL
     WHERE id = v_link.id;
    PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, 'link.request', v_link.id::text, jsonb_build_object('how', v_how, 'role', p_role));
    RETURN NULL;
  END IF;
  IF FOUND THEN
    v_id := v_link.id;
    UPDATE business_links SET status = 'requested', requested_by = v_me, requester_actor = p_actor_type || ':' || p_actor_id, how = v_how, role = p_role,
           requested_at = now(), expires_at = now() + interval '30 days', requester_hidden = false,
           answered_at = NULL, answered_actor = NULL, blocked_by = NULL, ended_by = NULL, ended_at = NULL
     WHERE id = v_id;
  ELSE
    v_id := gen_random_uuid();
    INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at)
    VALUES (v_id, LEAST(v_me, v_target), GREATEST(v_me, v_target), 'requested', v_me, p_actor_type || ':' || p_actor_id, v_how, p_role, now() + interval '30 days');
  END IF;
  PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, 'link.request', v_id::text, jsonb_build_object('how', v_how, 'role', p_role));
  PERFORM taskiem_link_audit_other(v_me, v_target, v_id, p_actor_type, p_actor_id, 'link.requested', jsonb_build_object('how', v_how));
  RETURN v_target;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_answer(p_link uuid, p_answer text, p_actor_type text, p_actor_id text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  l business_links%ROWTYPE;
  v_other uuid;
  v_view text;
  v_both boolean := true;
BEGIN
  SELECT * INTO l FROM business_links WHERE id = p_link AND v_me IN (a_tenant, b_tenant) FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such link' USING ERRCODE = 'P0002';
  END IF;
  v_other := CASE WHEN l.a_tenant = v_me THEN l.b_tenant ELSE l.a_tenant END;
  v_view := taskiem_link_view(l, v_me);
  IF p_answer IN ('accept', 'decline') AND v_view = 'asked_us' THEN
    UPDATE business_links SET status = CASE p_answer WHEN 'accept' THEN 'active' ELSE 'declined' END,
           answered_at = now(), answered_actor = p_actor_type || ':' || p_actor_id WHERE id = p_link;
    v_both := p_answer = 'accept';
  ELSIF p_answer = 'block' AND v_view IN ('asked_us', 'active', 'ended') THEN
    UPDATE business_links SET status = 'blocked', blocked_by = v_me, answered_at = COALESCE(answered_at, now()),
           ended_by = CASE WHEN l.status = 'active' THEN v_me ELSE ended_by END,
           ended_at = CASE WHEN l.status = 'active' THEN now() ELSE ended_at END WHERE id = p_link;
    v_both := l.status = 'active';
  ELSIF p_answer = 'unblock' AND v_view = 'blocked' THEN
    UPDATE business_links SET status = CASE WHEN ended_by IS NOT NULL THEN 'ended' ELSE 'declined' END, blocked_by = NULL WHERE id = p_link;
    v_both := false;
  ELSIF p_answer = 'cancel' AND v_view IN ('requested', 'not_accepted') THEN
    IF l.status = 'requested' THEN
      UPDATE business_links SET status = 'cancelled' WHERE id = p_link;
      v_both := l.expires_at > now();
    ELSE
      UPDATE business_links SET requester_hidden = true WHERE id = p_link;
      v_both := false;
    END IF;
  ELSIF p_answer = 'end' AND v_view = 'active' THEN
    UPDATE business_links SET status = 'ended', ended_by = v_me, ended_at = now() WHERE id = p_link;
  ELSE
    RAISE EXCEPTION 'the link cannot be answered that way now' USING ERRCODE = '55000';
  END IF;
  PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, 'link.' || p_answer, p_link::text, jsonb_build_object('business', v_other));
  IF v_both THEN
    PERFORM taskiem_link_audit_other(v_me, v_other, p_link, p_actor_type, p_actor_id, 'link.' || p_answer, '{}');
  END IF;
  RETURN v_other;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_link_blocked_by(business_links, uuid);
ALTER TABLE business_links
  DROP CONSTRAINT business_links_blocked_sides,
  DROP CONSTRAINT business_links_blocked_by_side,
  DROP COLUMN a_blocked,
  DROP COLUMN b_blocked;
