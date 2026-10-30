-- Business links, L0 (decision 0061; docs/spec/business-links.md §5).
--
-- A link is a relationship between two businesses on Taskiem, requested by
-- one and accepted by the other. It is a platform row, not a tenant's:
-- neither side reads business_links directly. Each side reads its links
-- through taskiem_links_of, which shows it only what it may know: a
-- requester never learns whether a request was declined, blocked or
-- reached no one (§5.1). A request is made by search (the business was
-- listed, so after 30 days it reads "not accepted") or blind, by a phone
-- number or a storefront address (it reads nothing but acceptance, ever).
--
-- The only crossing into the other side's tables is taskiem_link_enter,
-- modelled on taskiem_partner_enter: the link active, the caller a side,
-- an audit entry in both chains, and the scope narrowed to the other side
-- for the rest of the transaction (§5.3). L0 has no mirror to write yet;
-- bills (L1) are its first callers.
-- +goose Up
CREATE TABLE business_links (
  id              uuid PRIMARY KEY,
  a_tenant        uuid NOT NULL REFERENCES tenants(id),
  b_tenant        uuid NOT NULL REFERENCES tenants(id),
  status          text NOT NULL CHECK (status IN ('requested', 'active', 'declined', 'blocked', 'cancelled', 'ended')),
  -- The side that asked, how it asked, and how it sees the other.
  requested_by    uuid NOT NULL REFERENCES tenants(id),
  requester_actor text NOT NULL,
  how             text NOT NULL CHECK (how IN ('search', 'blind')),
  role            text NOT NULL CHECK (role IN ('supplier', 'customer', 'both')),
  requested_at    timestamptz NOT NULL DEFAULT now(),
  expires_at      timestamptz NOT NULL,
  -- A requester who cancelled a request it cannot know was blocked.
  requester_hidden boolean NOT NULL DEFAULT false,
  answered_at     timestamptz,
  answered_actor  text,
  blocked_by      uuid REFERENCES tenants(id),
  ended_by        uuid REFERENCES tenants(id),
  ended_at        timestamptz,
  UNIQUE (a_tenant, b_tenant),
  CHECK (a_tenant < b_tenant),
  CHECK (requested_by IN (a_tenant, b_tenant)),
  CHECK ((status = 'blocked') = (blocked_by IS NOT NULL))
);
CREATE INDEX business_links_b ON business_links (b_tenant);
CREATE INDEX business_links_requested ON business_links (requested_by, requested_at);
ALTER TABLE business_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE business_links FORCE ROW LEVEL SECURITY;
-- No business reads the table: only the definers below.
CREATE POLICY no_app ON business_links TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON business_links TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON business_links FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON business_links TO taskiem_dispatch;

-- A business's own link settings: whether other businesses find it by
-- search (NULL: the default, on with a published storefront, §5.1), and
-- when it was asked about it.
CREATE TABLE link_settings (
  tenant_id   uuid PRIMARY KEY REFERENCES tenants(id),
  findable    boolean,
  asked_at    timestamptz,
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE link_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON link_settings FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, UPDATE ON link_settings TO taskiem_app;
GRANT SELECT (tenant_id, findable) ON link_settings TO taskiem_dispatch;

-- A request sent blind, kept in the requester's own business: what it
-- sees of it whether or not a business is behind the address. Only the
-- masked address is kept, and a hash to send the same request once.
CREATE TABLE link_blind_requests (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  kind          text NOT NULL CHECK (kind IN ('phone', 'address')),
  shown         text NOT NULL CHECK (length(shown) BETWEEN 1 AND 40),
  address_hash  bytea NOT NULL CHECK (length(address_hash) = 32),
  role          text NOT NULL CHECK (role IN ('supplier', 'customer', 'both')),
  requested_by  text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  cancelled_at  timestamptz
);
CREATE INDEX link_blind_requests_tenant ON link_blind_requests (tenant_id, created_at);
ALTER TABLE link_blind_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_blind_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_blind_requests TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON link_blind_requests TO taskiem_app;

-- What the definers read of a business's public card.
GRANT SELECT (slug, published_slug) ON storefronts TO taskiem_dispatch;

-- The business a transaction acts for: exactly one active tenant.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_self() RETURNS uuid
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_scope uuid[] := taskiem_tenant_scope();
BEGIN
  IF cardinality(v_scope) <> 1 OR NOT EXISTS (SELECT 1 FROM tenants WHERE id = v_scope[1] AND status = 'active') THEN
    RAISE EXCEPTION 'a link acts for one active business' USING ERRCODE = '42501';
  END IF;
  RETURN v_scope[1];
END
$$;
-- +goose StatementEnd

-- An audit entry in the other side's chain, the caller's scope restored
-- after it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_audit_other(p_me uuid, p_other uuid, p_link uuid, p_actor_type text, p_actor_id text, p_action text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM set_config('app.tenant_scope', '{' || p_other::text || '}', true);
  PERFORM taskiem_audit_append(p_other, p_actor_type, 'link:' || p_me::text || '/' || p_actor_id, p_action, p_link::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('link', p_link, 'business', p_me));
  PERFORM set_config('app.tenant_scope', '{' || p_me::text || '}', true);
END
$$;
-- +goose StatementEnd

-- How one side sees a link: active, ended, requested (asked by us and
-- waiting), not_accepted (asked by us after search, 30 days gone without
-- acceptance, whatever the answer was), asked_us (waiting for our answer),
-- blocked (by us), or NULL (nothing to show). A requester never sees
-- declined or blocked; a blind request shows only once accepted (until
-- then the requester's own link_blind_requests row stands for it).
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_view(l business_links, p_me uuid) RETURNS text
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT CASE
    WHEN l.status = 'active' THEN 'active'
    WHEN l.status = 'ended' THEN 'ended'
    WHEN l.status = 'cancelled' THEN NULL
    WHEN l.status = 'blocked' AND l.blocked_by = p_me THEN 'blocked'
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

-- How a side sees the other: the requester chose it; the other side sees
-- its inverse.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_role(l business_links, p_me uuid) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE WHEN l.requested_by = p_me OR l.role = 'both' THEN l.role
              WHEN l.role = 'supplier' THEN 'customer' ELSE 'supplier' END
$$;
-- +goose StatementEnd

-- A business's links and requests, as it may see them: the other side's
-- name and public address, never its rows.
-- +goose StatementBegin
CREATE FUNCTION taskiem_links_of()
RETURNS TABLE (id uuid, business uuid, name text, slug text, state text, role text, asked_by_us boolean,
               requested_at timestamptz, expires_at timestamptz, answered_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH me AS (SELECT taskiem_link_self() AS id)
  SELECT l.id, o.id, o.name, (SELECT s.published_slug FROM storefronts s WHERE s.tenant_id = o.id AND s.status = 'published'),
         taskiem_link_view(l, me.id), taskiem_link_role(l, me.id), l.requested_by = me.id,
         l.requested_at, l.expires_at, CASE WHEN l.status = 'active' OR l.status = 'ended' THEN l.answered_at END
    FROM me, business_links l
    JOIN tenants o ON o.id = CASE WHEN l.a_tenant = (SELECT id FROM me) THEN l.b_tenant ELSE l.a_tenant END
   WHERE (l.a_tenant = me.id OR l.b_tenant = me.id) AND taskiem_link_view(l, me.id) IS NOT NULL
   ORDER BY taskiem_link_view(l, me.id) = 'asked_us' DESC, l.requested_at DESC
$$;
-- +goose StatementEnd

-- Whether a business lets others find it by search: its own choice, else
-- on with a published storefront.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_findable(p_tenant uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT ls.findable FROM link_settings ls WHERE ls.tenant_id = p_tenant),
                  EXISTS (SELECT 1 FROM storefronts s WHERE s.tenant_id = p_tenant AND s.status = 'published'))
     AND EXISTS (SELECT 1 FROM tenants t WHERE t.id = p_tenant AND t.status = 'active')
$$;
-- +goose StatementEnd

-- Search businesses that chose to be found, by name, by what they sell or
-- do, and by area (§5.1): every word must match the business's card or one
-- of its published items. Only public content: the published card and
-- catalogue. Linked businesses first, then those in the caller's area,
-- then by name. A query that holds a phone number finds nothing.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_search(p_query text, p_limit int)
RETURNS TABLE (business uuid, name text, slug text, area text, tagline text, items jsonb, state text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_words text[];
  v_area text;
BEGIN
  IF p_query ~ '\d{6,}' OR p_query ~ '(\d[\s-]*){7,}' THEN
    RETURN;
  END IF;
  SELECT array_agg(DISTINCT w) INTO v_words
    FROM regexp_split_to_table(lower(left(p_query, 100)), '[\s,]+') w
   WHERE length(w) >= 2 AND w NOT IN ('in', 'at', 'the', 'for', 'of', 'and', 'near', 'who', 'sells', 'sell', 'find');
  IF v_words IS NULL THEN
    RETURN;
  END IF;
  SELECT lower(snap.model->'card'->>'area') INTO v_area
    FROM storefronts s JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
   WHERE s.tenant_id = v_me AND s.status = 'published';
  RETURN QUERY
  WITH cand AS (
    SELECT t.id, COALESCE(NULLIF(snap.model->'card'->>'name', ''), t.name) AS name, s.published_slug AS slug,
           snap.model->'card'->>'area' AS area, snap.model->'card'->>'tagline' AS tagline, s.id AS sf,
           lower(concat_ws(' ', t.name, snap.model->'card'->>'name', snap.model->'card'->>'tagline', snap.model->'card'->>'area')) AS card
      FROM tenants t
      LEFT JOIN storefronts s ON s.tenant_id = t.id AND s.status = 'published'
      LEFT JOIN storefront_snapshots snap ON snap.storefront_id = s.id AND snap.version = s.published_version
     WHERE t.status = 'active' AND t.id <> v_me AND taskiem_link_findable(t.id)
  ), hit AS (
    SELECT c.*,
           (SELECT COALESCE(jsonb_agg(jsonb_build_object('name', x.item->>'name', 'price_kobo', x.item->'price_kobo') ORDER BY x.position), '[]')
              FROM (SELECT ci.item, ci.position FROM storefront_catalogue ci
                     WHERE ci.storefront_id = c.sf AND COALESCE((ci.item->>'available')::boolean, true)
                       AND EXISTS (SELECT 1 FROM unnest(v_words) w
                                    WHERE position(w IN lower(concat_ws(' ', ci.item->>'name', ci.item->>'category'))) > 0)
                     ORDER BY ci.position LIMIT 3) x) AS items
      FROM cand c
     WHERE NOT EXISTS (
       SELECT 1 FROM unnest(v_words) w
        WHERE position(w IN c.card) = 0
          AND NOT EXISTS (SELECT 1 FROM storefront_catalogue ci WHERE ci.storefront_id = c.sf
                             AND COALESCE((ci.item->>'available')::boolean, true)
                             AND position(w IN lower(concat_ws(' ', ci.item->>'name', ci.item->>'category'))) > 0))
  )
  SELECT h.id, h.name, h.slug, h.area, h.tagline, h.items,
         COALESCE((SELECT taskiem_link_view(l, v_me) FROM business_links l
                    WHERE l.a_tenant = LEAST(v_me, h.id) AND l.b_tenant = GREATEST(v_me, h.id)), 'none')
    FROM hit h
   ORDER BY EXISTS (SELECT 1 FROM business_links l WHERE l.a_tenant = LEAST(v_me, h.id) AND l.b_tenant = GREATEST(v_me, h.id) AND l.status = 'active') DESC,
            (v_area IS NOT NULL AND lower(h.area) = v_area) DESC, h.name, h.id
   LIMIT LEAST(GREATEST(p_limit, 1), 20);
END
$$;
-- +goose StatementEnd

-- Request a link (or ask again). By search, p_target is a business the
-- search listed: refused unless it lets itself be found. Blind, the
-- target is resolved here from a phone number (the owner's linked number
-- or the business's own WhatsApp number, when it names exactly one
-- business) or a storefront address, and the caller learns nothing: the
-- function returns the business told, or NULL, for the server to tell its
-- people; the server answers the person the same words either way.
-- A request to a business that blocked the caller is taken and dropped:
-- its clock starts again for the requester and no one is told. At most
-- 20 requests a day per business.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_request(p_target uuid, p_phone text, p_slug text, p_role text, p_actor_type text, p_actor_id text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_target uuid := p_target;
  v_how text := 'search';
  v_link business_links%ROWTYPE;
  v_id uuid;
  v_n int;
BEGIN
  IF p_role NOT IN ('supplier', 'customer', 'both') THEN
    RAISE EXCEPTION 'a link''s role is supplier, customer or both' USING ERRCODE = '22023';
  END IF;
  IF (SELECT count(*) FROM business_links WHERE requested_by = v_me AND requested_at > now() - interval '1 day') >= 20 THEN
    RAISE EXCEPTION 'too many link requests today' USING ERRCODE = '53400';
  END IF;
  IF p_target IS NULL THEN
    v_how := 'blind';
    IF p_phone IS NOT NULL THEN
      SELECT min(x.t::text)::uuid, count(DISTINCT x.t) INTO v_target, v_n FROM (
        SELECT m.tenant_id AS t FROM whatsapp_bindings b JOIN memberships m ON m.user_id = b.user_id AND m.role = 'owner'
         WHERE b.number = p_phone
        UNION
        SELECT n.tenant_id FROM whatsapp_numbers n WHERE n.display_number = p_phone AND n.status = 'active') x
        JOIN tenants t ON t.id = x.t AND t.status = 'active';
      IF v_n <> 1 THEN
        v_target := NULL;
      END IF;
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
    -- Ours, still within its 30 days (declined or not, as far as we know):
    -- shown again if we had cancelled it, and the other side not told twice.
    UPDATE business_links SET requester_hidden = false WHERE id = v_link.id;
    RETURN NULL;
  END IF;
  IF FOUND AND (v_link.status = 'active'
      OR (v_link.status = 'requested' AND v_link.expires_at > now() AND v_link.requested_by = v_target)) THEN
    -- Linked, or a request already waiting either way: nothing new.
    RETURN NULL;
  END IF;
  IF FOUND AND v_link.status = 'blocked' AND v_link.blocked_by = v_target THEN
    UPDATE business_links SET requested_by = v_me, requester_actor = p_actor_type || ':' || p_actor_id, how = v_how, role = p_role,
           requested_at = now(), expires_at = now() + interval '30 days', requester_hidden = false, ended_by = NULL, ended_at = NULL
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

-- Answer, cancel or end a link, as the side allowed to:
--   accept, decline: the side asked, while the request waits;
--   block: the side asked, or either side of a link (it ends);
--   unblock: the side that blocked;
--   cancel: the side that asked, while it waits (to the requester a
--     declined or blocked request still waits);
--   end: either side of an active link.
-- Returns the other side, audited in both chains (block, unblock and a
-- requester's cancel of a request it cannot know was refused: its own
-- chain only, so nothing reaches the other side).
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_answer(p_link uuid, p_answer text, p_actor_type text, p_actor_id text)
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
    -- Ending an active link is told; a block is not.
    v_both := l.status = 'active';
  ELSIF p_answer = 'unblock' AND v_view = 'blocked' THEN
    -- A link that was made goes back to ended. A request blocked becomes
    -- declined: to its requester it still waits out its 30 days, as it
    -- did while blocked, so the unblock is not told either.
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

-- The one door into the other side (§5.3): the link active, the caller a
-- side; the access audited in the caller's chain, the scope narrowed to
-- the other side for the rest of the transaction, and audited there too.
-- The caller writes its own rows before the call. Returns the other side.
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_enter(p_link uuid, p_actor_type text, p_actor_id text, p_action text, p_detail jsonb)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_other uuid;
BEGIN
  SELECT CASE WHEN l.a_tenant = v_me THEN l.b_tenant ELSE l.a_tenant END INTO v_other
    FROM business_links l JOIN tenants o ON o.id = CASE WHEN l.a_tenant = v_me THEN l.b_tenant ELSE l.a_tenant END AND o.status = 'active'
   WHERE l.id = p_link AND l.status = 'active' AND v_me IN (l.a_tenant, l.b_tenant);
  IF v_other IS NULL THEN
    RAISE EXCEPTION 'no such active link' USING ERRCODE = 'P0002';
  END IF;
  IF p_action !~ '^link\.[a-z_.]+$' THEN
    RAISE EXCEPTION 'a link action is named link.*' USING ERRCODE = '22023';
  END IF;
  PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, p_action, p_link::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('business', v_other));
  PERFORM set_config('app.tenant_scope', '{' || v_other::text || '}', true);
  PERFORM taskiem_audit_append(v_other, p_actor_type, 'link:' || v_me::text || '/' || p_actor_id, p_action, p_link::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('link', p_link, 'business', v_me));
  RETURN v_other;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_link_self()', 'taskiem_link_audit_other(uuid, uuid, uuid, text, text, text, jsonb)',
      'taskiem_link_view(business_links, uuid)', 'taskiem_link_role(business_links, uuid)', 'taskiem_links_of()',
      'taskiem_link_findable(uuid)', 'taskiem_link_search(text, int)', 'taskiem_link_request(uuid, text, text, text, text, text)',
      'taskiem_link_answer(uuid, text, text, text)', 'taskiem_link_enter(uuid, text, text, text, jsonb)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
  END LOOP;
  -- What the application calls; the helpers stay the definers' own.
  FOREACH f IN ARRAY ARRAY['taskiem_links_of()', 'taskiem_link_search(text, int)', 'taskiem_link_request(uuid, text, text, text, text, text)',
      'taskiem_link_answer(uuid, text, text, text)', 'taskiem_link_enter(uuid, text, text, text, jsonb)'] LOOP
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_link_enter(uuid, text, text, text, jsonb);
DROP FUNCTION taskiem_link_answer(uuid, text, text, text);
DROP FUNCTION taskiem_link_request(uuid, text, text, text, text, text);
DROP FUNCTION taskiem_link_search(text, int);
DROP FUNCTION taskiem_link_findable(uuid);
DROP FUNCTION taskiem_links_of();
DROP FUNCTION taskiem_link_role(business_links, uuid);
DROP FUNCTION taskiem_link_view(business_links, uuid);
DROP FUNCTION taskiem_link_audit_other(uuid, uuid, uuid, text, text, text, jsonb);
DROP FUNCTION taskiem_link_self();
DROP TABLE link_blind_requests;
DROP TABLE link_settings;
DROP TABLE business_links;
