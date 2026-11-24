-- Review fixes for stage 1 of Taskiem for everyone
-- (docs/spec/taskiem-for-everyone.md, "Review fixes: server").
--
-- * Chat sign-in no longer finds an account by a WhatsApp or Telegram
--   binding to a number that is not the account's phone (a number changed
--   away from signs no one in), and a chat sign-in checks the binding is
--   still the account's phone (taskiem_auth_chat_number_current).
-- * Second proofs: a usable password counts (§5.4), and so does a link to
--   a real email not yet confirmed; taskiem_auth_second_proofs says so.
--   user_devices.proved_by gains 'password'.
-- * The chat PIN as a second proof is checked under a row lock
--   (taskiem_wa_pin_lock), so parallel guesses cannot pass the lock.
-- * A phone change ends the person's other sessions and forgets other
--   devices' proofs (taskiem_auth_phone_change_fence).
-- * A change code another person asked for a number is not overwritten
--   ('busy').
-- * The person's passkeys, for a second-proof passkey challenge
--   (taskiem_auth_passkey_ids).
-- * Links: a number names only a business (not a workflows workspace),
--   and a link code or shop address never reaches a workflows workspace.
-- * Tenants started with "Just workflows for now" (every business area off
--   and Schools off) are workflows workspaces.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_chat_user(p_number text)
RETURNS TABLE (user_id uuid, status text, chat_signin boolean, whatsapp boolean, telegram_id bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH found AS (
    SELECT b.user_id, true AS wa, 1 AS rank FROM whatsapp_bindings b WHERE b.number = p_number
       AND NOT EXISTS (SELECT 1 FROM user_phones p WHERE p.user_id = b.user_id AND p.number <> p_number)
    UNION ALL
    SELECT t.user_id, false, 2 FROM telegram_bindings t WHERE t.number = p_number
       AND NOT EXISTS (SELECT 1 FROM user_phones p WHERE p.user_id = t.user_id AND p.number <> p_number)
  ), one AS (
    SELECT f.user_id, f.wa FROM found f ORDER BY f.rank LIMIT 1
  )
  SELECT u.id, u.status, u.chat_signin, o.wa, (SELECT t.telegram_id FROM telegram_bindings t WHERE t.user_id = u.id)
    FROM one o JOIN users u ON u.id = o.user_id
$$;
-- +goose StatementEnd

-- Whether the chat a person signs in with is still theirs: a WhatsApp
-- binding to a number other than the account's phone is not (the number
-- may be someone else's now). Telegram is bound to its own account, not a
-- number.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_chat_number_current(p_user uuid, p_channel text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p_channel <> 'whatsapp' OR NOT EXISTS (
    SELECT 1 FROM user_phones p JOIN whatsapp_bindings b ON b.user_id = p.user_id
     WHERE p.user_id = p_user AND p.number <> b.number)
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_auth_second_proofs(uuid);
-- The ways a person could prove themselves a second time: a passkey, a
-- real email (confirmed or not: the link goes to it), a chat PIN, and a
-- password (with whether the email is confirmed, which the API weighs).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_second_proofs(p_user uuid)
RETURNS TABLE (passkey boolean, email boolean, pin boolean, password boolean, email_verified boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = p_user),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.email NOT LIKE '%.taskiem.invalid'),
         EXISTS (SELECT 1 FROM whatsapp_pins p WHERE p.user_id = p_user),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.password_hash IS NOT NULL),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.email_verified_at IS NOT NULL)
$$;
-- +goose StatementEnd

ALTER TABLE user_devices DROP CONSTRAINT user_devices_proved_by_check;
ALTER TABLE user_devices ADD CONSTRAINT user_devices_proved_by_check CHECK (proved_by IS NULL OR proved_by IN ('passkey', 'email', 'pin', 'password'));

-- A person's PIN, its row locked until the caller's transaction ends: the
-- check and its count happen under the lock, so parallel guesses wait.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_pin_lock(p_user uuid)
RETURNS TABLE (pin_hash text, locked_until timestamptz)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.pin_hash, CASE WHEN p.locked_until > now() THEN p.locked_until END FROM whatsapp_pins p WHERE p.user_id = p_user FOR UPDATE
$$;
-- +goose StatementEnd

-- After a phone change: every other session of the person ends, and every
-- other device's second proof is forgotten. The count of sessions ended.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_change_fence(p_user uuid, p_keep_session bytea, p_keep_device bytea)
RETURNS bigint LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  UPDATE user_devices d SET proved_at = NULL, proved_by = NULL
   WHERE d.user_id = p_user AND d.proved_at IS NOT NULL AND d.device_hash IS DISTINCT FROM p_keep_device;
  WITH r AS (UPDATE sessions s SET revoked_at = now()
              WHERE s.user_id = p_user AND s.revoked_at IS NULL AND s.token_hash IS DISTINCT FROM p_keep_session RETURNING 1)
  SELECT count(*) INTO n FROM r;
  RETURN n;
END
$$;
-- +goose StatementEnd

-- The ids of a person's passkeys, for a second proof's challenge.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_passkey_ids(p_user uuid)
RETURNS bytea[] LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(array_agg(c.id ORDER BY c.id), '{}') FROM webauthn_credentials c WHERE c.user_id = p_user
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_phone_code_issue(p_number text, p_purpose text, p_user uuid, p_code text, p_via text, p_ttl interval, p_max_sends int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r phone_codes;
  h bytea := sha256(convert_to(p_purpose || ':' || p_number || ':' || p_code, 'UTF8'));
BEGIN
  SELECT * INTO r FROM phone_codes c WHERE c.number = p_number AND c.purpose = p_purpose FOR UPDATE;
  IF NOT FOUND THEN
    INSERT INTO phone_codes (number, purpose, user_id, code_hash, sent_via, expires_at) VALUES (p_number, p_purpose, p_user, h, p_via, now() + p_ttl);
    RETURN 'ok';
  END IF;
  IF r.locked_until > now() THEN
    RETURN 'locked';
  END IF;
  -- A change code another person asked for this number, still pending or
  -- proved, is theirs: it is not overwritten.
  IF p_purpose = 'change_new' AND r.user_id IS DISTINCT FROM p_user
     AND (r.expires_at > now() OR r.proven_at > now() - interval '15 minutes') THEN
    RETURN 'busy';
  END IF;
  IF r.window_start > now() - interval '1 hour' THEN
    IF r.sends >= p_max_sends THEN
      RETURN 'too_many';
    END IF;
    UPDATE phone_codes c SET user_id = p_user, code_hash = h, sent_via = p_via, created_at = now(), expires_at = now() + p_ttl,
      sends = c.sends + 1, proven_at = NULL, ticket_hash = NULL, ticket_expires_at = NULL
     WHERE c.number = p_number AND c.purpose = p_purpose;
  ELSE
    UPDATE phone_codes c SET user_id = p_user, code_hash = h, sent_via = p_via, created_at = now(), expires_at = now() + p_ttl,
      sends = 1, window_start = now(), failures = 0, proven_at = NULL, ticket_hash = NULL, ticket_expires_at = NULL
     WHERE c.number = p_number AND c.purpose = p_purpose;
  END IF;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_phone_business(p_phone text) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT CASE WHEN count(DISTINCT x.t) = 1 THEN min(x.t::text)::uuid END FROM (
    SELECT m.tenant_id AS t FROM whatsapp_bindings b JOIN memberships m ON m.user_id = b.user_id AND m.role = 'owner'
     WHERE b.number = p_phone
    UNION
    SELECT n.tenant_id FROM whatsapp_numbers n WHERE n.display_number = p_phone AND n.status = 'active') x
    JOIN tenants t ON t.id = x.t AND t.status = 'active' AND t.kind = 'business'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_by_code(p_code text)
RETURNS TABLE (business uuid, name text, slug text, state text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH me AS (SELECT taskiem_link_self() AS id)
  SELECT t.id, t.name, (SELECT s.published_slug FROM storefronts s WHERE s.tenant_id = t.id AND s.status = 'published'),
         COALESCE((SELECT taskiem_link_view(l, me.id) FROM business_links l
                    WHERE l.a_tenant = LEAST(me.id, t.id) AND l.b_tenant = GREATEST(me.id, t.id)), 'none')
    FROM me, link_settings ls JOIN tenants t ON t.id = ls.tenant_id AND t.status = 'active' AND t.kind = 'business'
   WHERE ls.code = lower(p_code) AND ls.tenant_id <> me.id
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
  IF p_code IS NOT NULL THEN
    v_how := 'code';
    SELECT ls.tenant_id INTO v_target FROM link_settings ls JOIN tenants t ON t.id = ls.tenant_id AND t.status = 'active' AND t.kind = 'business'
     WHERE ls.code = lower(p_code);
    IF v_target IS NULL OR v_target = v_me THEN
      RAISE EXCEPTION 'no such business' USING ERRCODE = 'P0002';
    END IF;
  ELSIF p_target IS NULL THEN
    v_how := 'blind';
    IF p_phone IS NOT NULL THEN
      v_target := taskiem_link_phone_business(p_phone);
    ELSIF p_slug IS NOT NULL THEN
      SELECT s.tenant_id INTO v_target FROM storefronts s JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active' AND t.kind = 'business'
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

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_auth_chat_number_current(uuid, text)', 'taskiem_auth_second_proofs(uuid)', 'taskiem_wa_pin_lock(uuid)',
      'taskiem_auth_phone_change_fence(uuid, bytea, bytea)', 'taskiem_auth_passkey_ids(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- "Just workflows for now" sign-ups (every business area off, Schools
-- off) are workflows workspaces (§18.2).
UPDATE tenants t SET kind = 'workflows'
 WHERE t.kind = 'business'
   AND (SELECT count(*) FROM business_areas b WHERE b.tenant_id = t.id AND NOT b.enabled
          AND b.area IN ('storefront', 'orders', 'stock', 'money', 'links')) = 5
   AND NOT EXISTS (SELECT 1 FROM school_settings s WHERE s.tenant_id = t.id AND s.enabled);

-- +goose Down
-- The kinds set above stay: a workflows workspace with every area off is
-- what those tenants are, and 00449's Down drops the column anyway.
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_by_code(p_code text)
RETURNS TABLE (business uuid, name text, slug text, state text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH me AS (SELECT taskiem_link_self() AS id)
  SELECT t.id, t.name, (SELECT s.published_slug FROM storefronts s WHERE s.tenant_id = t.id AND s.status = 'published'),
         COALESCE((SELECT taskiem_link_view(l, me.id) FROM business_links l
                    WHERE l.a_tenant = LEAST(me.id, t.id) AND l.b_tenant = GREATEST(me.id, t.id)), 'none')
    FROM me, link_settings ls JOIN tenants t ON t.id = ls.tenant_id AND t.status = 'active'
   WHERE ls.code = lower(p_code) AND ls.tenant_id <> me.id
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_phone_business(p_phone text) RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT CASE WHEN count(DISTINCT x.t) = 1 THEN min(x.t::text)::uuid END FROM (
    SELECT m.tenant_id AS t FROM whatsapp_bindings b JOIN memberships m ON m.user_id = b.user_id AND m.role = 'owner'
     WHERE b.number = p_phone
    UNION
    SELECT n.tenant_id FROM whatsapp_numbers n WHERE n.display_number = p_phone AND n.status = 'active') x
    JOIN tenants t ON t.id = x.t AND t.status = 'active'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_phone_code_issue(p_number text, p_purpose text, p_user uuid, p_code text, p_via text, p_ttl interval, p_max_sends int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r phone_codes;
  h bytea := sha256(convert_to(p_purpose || ':' || p_number || ':' || p_code, 'UTF8'));
BEGIN
  SELECT * INTO r FROM phone_codes c WHERE c.number = p_number AND c.purpose = p_purpose FOR UPDATE;
  IF NOT FOUND THEN
    INSERT INTO phone_codes (number, purpose, user_id, code_hash, sent_via, expires_at) VALUES (p_number, p_purpose, p_user, h, p_via, now() + p_ttl);
    RETURN 'ok';
  END IF;
  IF r.locked_until > now() THEN
    RETURN 'locked';
  END IF;
  IF r.window_start > now() - interval '1 hour' THEN
    IF r.sends >= p_max_sends THEN
      RETURN 'too_many';
    END IF;
    UPDATE phone_codes c SET user_id = p_user, code_hash = h, sent_via = p_via, created_at = now(), expires_at = now() + p_ttl,
      sends = c.sends + 1, proven_at = NULL, ticket_hash = NULL, ticket_expires_at = NULL
     WHERE c.number = p_number AND c.purpose = p_purpose;
  ELSE
    UPDATE phone_codes c SET user_id = p_user, code_hash = h, sent_via = p_via, created_at = now(), expires_at = now() + p_ttl,
      sends = 1, window_start = now(), failures = 0, proven_at = NULL, ticket_hash = NULL, ticket_expires_at = NULL
     WHERE c.number = p_number AND c.purpose = p_purpose;
  END IF;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_auth_passkey_ids(uuid);
DROP FUNCTION taskiem_auth_phone_change_fence(uuid, bytea, bytea);
DROP FUNCTION taskiem_wa_pin_lock(uuid);
UPDATE user_devices SET proved_by = 'pin' WHERE proved_by = 'password';
ALTER TABLE user_devices DROP CONSTRAINT user_devices_proved_by_check;
ALTER TABLE user_devices ADD CONSTRAINT user_devices_proved_by_check CHECK (proved_by IS NULL OR proved_by IN ('passkey', 'email', 'pin'));
DROP FUNCTION taskiem_auth_second_proofs(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_second_proofs(p_user uuid)
RETURNS TABLE (passkey boolean, email boolean, pin boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = p_user),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.email NOT LIKE '%.taskiem.invalid' AND u.email_verified_at IS NOT NULL),
         EXISTS (SELECT 1 FROM whatsapp_pins p WHERE p.user_id = p_user)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
BEGIN
  ALTER FUNCTION taskiem_auth_second_proofs(uuid) OWNER TO taskiem_dispatch;
  REVOKE EXECUTE ON FUNCTION taskiem_auth_second_proofs(uuid) FROM PUBLIC;
  GRANT EXECUTE ON FUNCTION taskiem_auth_second_proofs(uuid) TO taskiem_app;
END $$;
-- +goose StatementEnd
DROP FUNCTION taskiem_auth_chat_number_current(uuid, text);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_chat_user(p_number text)
RETURNS TABLE (user_id uuid, status text, chat_signin boolean, whatsapp boolean, telegram_id bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH found AS (
    SELECT b.user_id, true AS wa, 1 AS rank FROM whatsapp_bindings b WHERE b.number = p_number
    UNION ALL
    SELECT t.user_id, false, 2 FROM telegram_bindings t WHERE t.number = p_number
  ), one AS (
    SELECT f.user_id, f.wa FROM found f ORDER BY f.rank LIMIT 1
  )
  SELECT u.id, u.status, u.chat_signin, o.wa, (SELECT t.telegram_id FROM telegram_bindings t WHERE t.user_id = u.id)
    FROM one o JOIN users u ON u.id = o.user_id
$$;
-- +goose StatementEnd
