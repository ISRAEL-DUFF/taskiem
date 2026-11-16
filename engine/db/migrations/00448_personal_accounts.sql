-- Personal accounts (docs/spec/taskiem-for-everyone.md §5, stage 1,
-- decision 0068). A person signs in with their phone number and a code,
-- and may belong to no business: their session then has no tenant (a
-- personal session), which reaches only their own routes. Switching into a
-- business checks that business's rules, and an account in a business
-- signing in by phone code on a new device needs a second proof first
-- (§5.4): a passkey, a link to its email, or the chat PIN.
--
-- Everything here is a person's, not a business's: no row is reachable by
-- the app role, only through the definers below, as chat sign-in codes are
-- (00314). Numbers are kept as whatsapp_bindings keeps them (E.164 text);
-- the spec's sealed number under the person's subject is left for later
-- (its as-built notes say so).

-- +goose Up

-- A personal session has no tenant; 'phone' is signing in with a code to
-- the person's phone (WhatsApp or SMS).
ALTER TABLE sessions ALTER COLUMN tenant_id DROP NOT NULL;
ALTER TABLE sessions DROP CONSTRAINT sessions_auth_method_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_auth_method_check CHECK (auth_method IN ('password', 'passkey', 'sso', 'chat', 'phone'));
CREATE POLICY dispatch_insert ON sessions FOR INSERT TO taskiem_dispatch WITH CHECK (true);
GRANT INSERT ON sessions TO taskiem_dispatch;
GRANT SELECT (ip, device_name, device_platform) ON sessions TO taskiem_dispatch;

-- When a person made by phone confirmed they are 18 or over (§15).
ALTER TABLE users ADD COLUMN adult_confirmed_at timestamptz;

-- A person's phone: one per account, unique across accounts.
CREATE TABLE user_phones (
  user_id         uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  number          text NOT NULL UNIQUE CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  verified_at     timestamptz NOT NULL DEFAULT now(),
  last_signin_at  timestamptz
);

-- Codes for the phone way in and for changing a phone, kept only as a
-- SHA-256 of purpose:number:code. One pending code per number and purpose;
-- at most p_max_sends an hour; five wrong tries, across codes, lock the
-- number for an hour. A right sign-in code leaves a ticket (its hash) for
-- making an account on a new number; a right change code leaves proof
-- (proven_at) that the change uses up.
CREATE TABLE phone_codes (
  number             text NOT NULL CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  purpose            text NOT NULL CHECK (purpose IN ('sign_in', 'change_new', 'change_old')),
  user_id            uuid REFERENCES users(id) ON DELETE CASCADE,
  code_hash          bytea NOT NULL CHECK (length(code_hash) = 32),
  sent_via           text NOT NULL CHECK (sent_via IN ('whatsapp', 'sms')),
  created_at         timestamptz NOT NULL DEFAULT now(),
  expires_at         timestamptz NOT NULL,
  failures           int NOT NULL DEFAULT 0,
  sends              int NOT NULL DEFAULT 1,
  window_start       timestamptz NOT NULL DEFAULT now(),
  locked_until       timestamptz,
  proven_at          timestamptz,
  ticket_hash        bytea UNIQUE CHECK (ticket_hash IS NULL OR length(ticket_hash) = 32),
  ticket_expires_at  timestamptz,
  PRIMARY KEY (number, purpose)
);

-- The devices a person signed in on, and whether each passed a second
-- proof. A device is the phone app's install, or a browser's long-lived
-- cookie, kept as a SHA-256.
CREATE TABLE user_devices (
  user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_hash     bytea NOT NULL CHECK (length(device_hash) = 32),
  first_seen_at   timestamptz NOT NULL DEFAULT now(),
  last_signin_at  timestamptz NOT NULL DEFAULT now(),
  proved_at       timestamptz,
  proved_by       text CHECK (proved_by IS NULL OR proved_by IN ('passkey', 'email', 'pin')),
  PRIMARY KEY (user_id, device_hash)
);

-- A second proof by email: a one-time link for the device that asked.
CREATE TABLE second_proof_links (
  token_hash   bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_hash  bytea NOT NULL CHECK (length(device_hash) = 32),
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  used_at      timestamptz
);
CREATE INDEX second_proof_links_user ON second_proof_links (user_id, created_at);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['user_phones', 'phone_codes', 'user_devices', 'second_proof_links'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY no_direct_access ON %I TO taskiem_app USING (false) WITH CHECK (false)', t);
    EXECUTE format('CREATE POLICY dispatch ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', t);
    EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_dispatch', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- Existing numbers (§5.3): a WhatsApp number bound to an account is its
-- phone (a number is bound to one account at most, and an account to one
-- number); then a number a Telegram person shared, where free. Bindings
-- gone dormant are left out: the number may be someone else's now.
INSERT INTO user_phones (user_id, number, verified_at)
SELECT b.user_id, b.number, b.verified_at FROM whatsapp_bindings b WHERE b.dormant_at IS NULL;
INSERT INTO user_phones (user_id, number, verified_at)
SELECT t.user_id, t.number, t.verified_at FROM telegram_bindings t
 WHERE t.number IS NOT NULL AND t.dormant_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM user_phones p WHERE p.user_id = t.user_id OR p.number = t.number);

-- A session: a personal one has no tenant; a business's tenant must still
-- be active.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_session(p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid, auth_method text, signed_in_at timestamptz, device boolean, last_used_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, s.tenant_id, s.auth_method, s.created_at, s.device_id IS NOT NULL, s.last_used_at FROM sessions s
    LEFT JOIN tenants t ON t.id = s.tenant_id
    JOIN users u ON u.id = s.user_id AND u.status = 'active'
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
     AND (s.tenant_id IS NULL OR t.status = 'active')
$$;
-- +goose StatementEnd

-- Starts a personal session for an active person, ending their earlier
-- live sessions on the same phone (in any business) first.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_personal_start(p_hash bytea, p_user uuid, p_method text, p_ttl interval, p_ip text,
  p_device uuid, p_device_name text, p_platform text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.status = 'active') THEN
    RETURN false;
  END IF;
  IF p_device IS NOT NULL THEN
    UPDATE sessions s SET revoked_at = now() WHERE s.user_id = p_user AND s.device_id = p_device AND s.revoked_at IS NULL;
  END IF;
  INSERT INTO sessions (token_hash, user_id, tenant_id, expires_at, ip, auth_method, device_id, device_name, device_platform, last_used_at)
  VALUES (p_hash, p_user, NULL, now() + p_ttl, p_ip, p_method, p_device, p_device_name, p_platform, CASE WHEN p_device IS NOT NULL THEN now() END);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- Ends a person's live sessions on one phone, in every business and on
-- their own side (signing in again on a phone).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_device_end(p_user uuid, p_device uuid)
RETURNS bigint LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH r AS (UPDATE sessions s SET revoked_at = now()
              WHERE s.user_id = p_user AND s.device_id = p_device AND s.revoked_at IS NULL RETURNING 1)
  SELECT count(*) FROM r
$$;
-- +goose StatementEnd

-- Ends one session by its token's hash (signing out of a personal one).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_session_end(p_hash bytea)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE sessions s SET revoked_at = now() WHERE s.token_hash = p_hash AND s.revoked_at IS NULL
$$;
-- +goose StatementEnd

-- Switching (§5.4): a live session ends and its twin takes its place, in
-- the business given (where the person is a member and the business is
-- active) or on the person's own side (NULL). The same moment of signing
-- in, method, phone and expiry. False when the session is not live or the
-- person is not a member there. The API checks the business's rules first.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_session_move(p_old bytea, p_new bytea, p_tenant uuid, p_ip text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  WITH old AS (
    UPDATE sessions s SET revoked_at = now()
     WHERE s.token_hash = p_old AND s.revoked_at IS NULL AND s.expires_at > now()
       AND (p_tenant IS NULL OR EXISTS (SELECT 1 FROM memberships m JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
                                         WHERE m.user_id = s.user_id AND m.tenant_id = p_tenant))
    RETURNING s.user_id, s.created_at, s.expires_at, s.auth_method, s.device_id, s.device_name, s.device_platform, s.last_used_at)
  INSERT INTO sessions (token_hash, user_id, tenant_id, created_at, expires_at, ip, auth_method, device_id, device_name, device_platform, last_used_at)
  SELECT p_new, o.user_id, p_tenant, o.created_at, o.expires_at, p_ip, o.auth_method, o.device_id, o.device_name, o.device_platform, o.last_used_at FROM old o;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n = 1;
END
$$;
-- +goose StatementEnd

-- Who a number signs in (§5.1, §5.3): the account whose phone it is, else
-- the one whose WhatsApp is bound to it, else the one whose Telegram
-- shared it (dormant bindings left out). Their status and whether they
-- allow signing in by a code to their phone or chat (users.chat_signin).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_user(p_number text)
RETURNS TABLE (user_id uuid, status text, chat_signin boolean, has_phone boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH found AS (
    SELECT p.user_id, 1 AS rank FROM user_phones p WHERE p.number = p_number
    UNION ALL
    SELECT b.user_id, 2 FROM whatsapp_bindings b WHERE b.number = p_number AND b.dormant_at IS NULL
    UNION ALL
    SELECT t.user_id, 3 FROM telegram_bindings t WHERE t.number = p_number AND t.dormant_at IS NULL
  ), one AS (SELECT f.user_id FROM found f ORDER BY f.rank LIMIT 1)
  SELECT u.id, u.status, u.chat_signin, EXISTS (SELECT 1 FROM user_phones p WHERE p.user_id = u.id)
    FROM one o JOIN users u ON u.id = o.user_id
$$;
-- +goose StatementEnd

-- A person's phone number, for their own account page.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_of(p_user uuid)
RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.number FROM user_phones p WHERE p.user_id = p_user
$$;
-- +goose StatementEnd

-- Makes a number a person's phone where it is free and they have none
-- (an account found by its binding, signing in by phone): ok, taken or has.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_claim(p_user uuid, p_number text)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM user_phones p WHERE p.user_id = p_user) THEN
    RETURN 'has';
  END IF;
  IF EXISTS (SELECT 1 FROM user_phones p WHERE p.number = p_number)
     OR EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = p_number AND b.user_id <> p_user)
     OR EXISTS (SELECT 1 FROM telegram_bindings t WHERE t.number = p_number AND t.user_id <> p_user) THEN
    RETURN 'taken';
  END IF;
  INSERT INTO user_phones (user_id, number) VALUES (p_user, p_number);
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- Issues a code: ok, too_many (p_max_sends this hour) or locked. A new
-- code replaces the pending one; wrong tries are kept until the lock or a
-- right code.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_code_issue(p_number text, p_purpose text, p_user uuid, p_code text, p_via text, p_ttl interval, p_max_sends int)
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

-- Checks a code typed for a number: ok (the code is spent and proven; a
-- sign-in code keeps p_ticket for 15 minutes), none, expired, locked or
-- wrong (counted; the p_max_failures-th wrong try locks the number for an
-- hour). A code asked for by a person (changing a phone) is good only for
-- them.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_code_check(p_number text, p_purpose text, p_user uuid, p_code text, p_max_failures int, p_ticket bytea)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r phone_codes;
BEGIN
  SELECT * INTO r FROM phone_codes c WHERE c.number = p_number AND c.purpose = p_purpose FOR UPDATE;
  IF NOT FOUND OR r.user_id IS DISTINCT FROM p_user THEN
    RETURN 'none';
  END IF;
  IF r.locked_until > now() THEN
    RETURN 'locked';
  END IF;
  IF r.expires_at <= now() THEN
    RETURN 'expired';
  END IF;
  IF r.code_hash <> sha256(convert_to(p_purpose || ':' || p_number || ':' || p_code, 'UTF8')) THEN
    IF r.failures + 1 >= p_max_failures THEN
      UPDATE phone_codes c SET failures = 0, locked_until = now() + interval '1 hour', expires_at = now()
       WHERE c.number = p_number AND c.purpose = p_purpose;
      RETURN 'locked';
    END IF;
    UPDATE phone_codes c SET failures = c.failures + 1 WHERE c.number = p_number AND c.purpose = p_purpose;
    RETURN 'wrong';
  END IF;
  UPDATE phone_codes c SET expires_at = now(), failures = 0, proven_at = now(),
    ticket_hash = CASE WHEN p_purpose = 'sign_in' THEN p_ticket END,
    ticket_expires_at = CASE WHEN p_purpose = 'sign_in' AND p_ticket IS NOT NULL THEN now() + interval '15 minutes' END
   WHERE c.number = p_number AND c.purpose = p_purpose;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- Uses a ticket for a new account once: the number it proved, or no row.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_ticket_use(p_ticket bytea)
RETURNS text LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE phone_codes c SET ticket_hash = NULL, ticket_expires_at = NULL
   WHERE c.ticket_hash = p_ticket AND c.purpose = 'sign_in' AND c.ticket_expires_at > now()
  RETURNING c.number
$$;
-- +goose StatementEnd

-- Whether a number belongs to any account (a phone, or a live binding).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_taken(p_number text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM user_phones p WHERE p.number = p_number)
      OR EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = p_number)
      OR EXISTS (SELECT 1 FROM telegram_bindings t WHERE t.number = p_number)
$$;
-- +goose StatementEnd

-- Changes a person's phone (§5.5) to a number whose change code they
-- proved in the last 15 minutes, and that belongs to no other account:
-- ok, unproven or taken. The proof is used up.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_change(p_user uuid, p_number text)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM phone_codes c WHERE c.number = p_number AND c.purpose = 'change_new' AND c.user_id = p_user
     AND c.proven_at > now() - interval '15 minutes';
  IF NOT FOUND THEN
    RETURN 'unproven';
  END IF;
  IF EXISTS (SELECT 1 FROM user_phones p WHERE p.number = p_number AND p.user_id <> p_user)
     OR EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = p_number AND b.user_id <> p_user)
     OR EXISTS (SELECT 1 FROM telegram_bindings t WHERE t.number = p_number AND t.user_id <> p_user) THEN
    RETURN 'taken';
  END IF;
  INSERT INTO user_phones AS p (user_id, number) VALUES (p_user, p_number)
  ON CONFLICT (user_id) DO UPDATE SET number = EXCLUDED.number, verified_at = now(), last_signin_at = NULL;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- Whether the person proved a code sent to their own (old) number for a
-- change in the last 15 minutes; the proof is used up.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_old_proven(p_user uuid)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH d AS (DELETE FROM phone_codes c USING user_phones p
              WHERE p.user_id = p_user AND c.number = p.number AND c.purpose = 'change_old' AND c.user_id = p_user
                AND c.proven_at > now() - interval '15 minutes' RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM d)
$$;
-- +goose StatementEnd

-- A sign-in on a device: remembered, and whether it has passed a second
-- proof. A device that has not signed in for 90 days, or any device of an
-- account whose phone has not signed in for 90 days (when this sign-in is
-- by phone), is new again: its proof is forgotten (§5.4).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_device_signin(p_user uuid, p_device bytea, p_phone boolean)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_stale boolean := false;
  v_proved timestamptz;
BEGIN
  IF p_phone THEN
    SELECT p.last_signin_at < now() - interval '90 days' INTO v_stale FROM user_phones p WHERE p.user_id = p_user;
    UPDATE user_phones p SET last_signin_at = now() WHERE p.user_id = p_user;
  END IF;
  IF p_device IS NULL THEN
    RETURN false;
  END IF;
  INSERT INTO user_devices AS d (user_id, device_hash) VALUES (p_user, p_device)
  ON CONFLICT (user_id, device_hash) DO UPDATE
    SET proved_at = CASE WHEN d.last_signin_at < now() - interval '90 days' OR coalesce(v_stale, false) THEN NULL ELSE d.proved_at END,
        proved_by = CASE WHEN d.last_signin_at < now() - interval '90 days' OR coalesce(v_stale, false) THEN NULL ELSE d.proved_by END,
        last_signin_at = now()
  RETURNING d.proved_at INTO v_proved;
  RETURN v_proved IS NOT NULL;
END
$$;
-- +goose StatementEnd

-- Whether a person's device has passed a second proof.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_device_proved(p_user uuid, p_device bytea)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM user_devices d WHERE d.user_id = p_user AND d.device_hash = p_device AND d.proved_at IS NOT NULL)
$$;
-- +goose StatementEnd

-- Records a second proof for a person's device.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_device_prove(p_user uuid, p_device bytea, p_how text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO user_devices AS d (user_id, device_hash, proved_at, proved_by) VALUES (p_user, p_device, now(), p_how)
  ON CONFLICT (user_id, device_hash) DO UPDATE SET proved_at = now(), proved_by = EXCLUDED.proved_by
$$;
-- +goose StatementEnd

-- Issues a second-proof link for a device: false when the person asked
-- for p_max this hour.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_proof_link_issue(p_user uuid, p_hash bytea, p_device bytea, p_ttl interval, p_max int)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF (SELECT count(*) FROM second_proof_links l WHERE l.user_id = p_user AND l.created_at > now() - interval '1 hour') >= p_max THEN
    RETURN false;
  END IF;
  DELETE FROM second_proof_links l WHERE l.user_id = p_user AND l.created_at < now() - interval '1 day';
  INSERT INTO second_proof_links (token_hash, user_id, device_hash, expires_at) VALUES (p_hash, p_user, p_device, now() + p_ttl);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- Uses a second-proof link once, for its own person and the device that
-- asked for it: true when it did, and the device is then proved.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_proof_link_use(p_hash bytea, p_user uuid, p_device bytea)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  UPDATE second_proof_links l SET used_at = now()
   WHERE l.token_hash = p_hash AND l.user_id = p_user AND l.device_hash = p_device AND l.used_at IS NULL AND l.expires_at > now();
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  PERFORM taskiem_auth_device_prove(p_user, p_device, 'email');
  RETURN true;
END
$$;
-- +goose StatementEnd

-- The ways a person could prove themselves a second time: a passkey, a
-- real (confirmed) email, a chat PIN.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_second_proofs(p_user uuid)
RETURNS TABLE (passkey boolean, email boolean, pin boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = p_user),
         EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.email NOT LIKE '%.taskiem.invalid' AND u.email_verified_at IS NOT NULL),
         EXISTS (SELECT 1 FROM whatsapp_pins p WHERE p.user_id = p_user)
$$;
-- +goose StatementEnd
GRANT SELECT (email_verified_at) ON users TO taskiem_dispatch;

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY[
    'taskiem_auth_personal_start(bytea, uuid, text, interval, text, uuid, text, text)', 'taskiem_auth_device_end(uuid, uuid)',
    'taskiem_auth_session_end(bytea)', 'taskiem_auth_session_move(bytea, bytea, uuid, text)',
    'taskiem_auth_phone_user(text)', 'taskiem_auth_phone_of(uuid)', 'taskiem_auth_phone_claim(uuid, text)',
    'taskiem_auth_phone_code_issue(text, text, uuid, text, text, interval, int)', 'taskiem_auth_phone_code_check(text, text, uuid, text, int, bytea)',
    'taskiem_auth_phone_ticket_use(bytea)', 'taskiem_auth_phone_taken(text)', 'taskiem_auth_phone_change(uuid, text)',
    'taskiem_auth_phone_old_proven(uuid)', 'taskiem_auth_device_signin(uuid, bytea, boolean)', 'taskiem_auth_device_proved(uuid, bytea)',
    'taskiem_auth_device_prove(uuid, bytea, text)', 'taskiem_auth_proof_link_issue(uuid, bytea, bytea, interval, int)',
    'taskiem_auth_proof_link_use(bytea, uuid, bytea)', 'taskiem_auth_second_proofs(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_auth_second_proofs(uuid);
REVOKE SELECT (email_verified_at) ON users FROM taskiem_dispatch;
DROP FUNCTION taskiem_auth_proof_link_use(bytea, uuid, bytea);
DROP FUNCTION taskiem_auth_proof_link_issue(uuid, bytea, bytea, interval, int);
DROP FUNCTION taskiem_auth_device_prove(uuid, bytea, text);
DROP FUNCTION taskiem_auth_device_proved(uuid, bytea);
DROP FUNCTION taskiem_auth_device_signin(uuid, bytea, boolean);
DROP FUNCTION taskiem_auth_phone_old_proven(uuid);
DROP FUNCTION taskiem_auth_phone_change(uuid, text);
DROP FUNCTION taskiem_auth_phone_taken(text);
DROP FUNCTION taskiem_auth_phone_ticket_use(bytea);
DROP FUNCTION taskiem_auth_phone_code_check(text, text, uuid, text, int, bytea);
DROP FUNCTION taskiem_auth_phone_code_issue(text, text, uuid, text, text, interval, int);
DROP FUNCTION taskiem_auth_phone_claim(uuid, text);
DROP FUNCTION taskiem_auth_phone_of(uuid);
DROP FUNCTION taskiem_auth_phone_user(text);
DROP FUNCTION taskiem_auth_session_move(bytea, bytea, uuid, text);
DROP FUNCTION taskiem_auth_session_end(bytea);
DROP FUNCTION taskiem_auth_device_end(uuid, uuid);
DROP FUNCTION taskiem_auth_personal_start(bytea, uuid, text, interval, text, uuid, text, text);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_session(p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid, auth_method text, signed_in_at timestamptz, device boolean, last_used_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, s.tenant_id, s.auth_method, s.created_at, s.device_id IS NOT NULL, s.last_used_at FROM sessions s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
    JOIN users u ON u.id = s.user_id AND u.status = 'active'
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd
DROP TABLE second_proof_links;
DROP TABLE user_devices;
DROP TABLE phone_codes;
DROP TABLE user_phones;
ALTER TABLE users DROP COLUMN adult_confirmed_at;
-- Personal sessions and phone sessions have no place without these: gone.
DELETE FROM sessions WHERE tenant_id IS NULL OR auth_method = 'phone';
REVOKE SELECT (ip, device_name, device_platform) ON sessions FROM taskiem_dispatch;
REVOKE INSERT ON sessions FROM taskiem_dispatch;
DROP POLICY dispatch_insert ON sessions;
ALTER TABLE sessions DROP CONSTRAINT sessions_auth_method_check;
ALTER TABLE sessions ADD CONSTRAINT sessions_auth_method_check CHECK (auth_method IN ('password', 'passkey', 'sso', 'chat'));
ALTER TABLE sessions ALTER COLUMN tenant_id SET NOT NULL;
