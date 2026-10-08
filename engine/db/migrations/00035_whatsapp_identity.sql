-- WhatsApp as a client of the platform (spec 11.2): a person binds one
-- WhatsApp number to their account by a code sent to it, and every message
-- from that number then speaks for them. Bindings, pending codes, the
-- per-number conversation window and inbound message ids belong to people
-- and numbers, not to tenants: no tenant reads them directly. Only the
-- functions below reach them, each doing one narrow thing.

-- +goose Up
-- A number is in E.164 form: + and up to 15 digits.
CREATE TABLE whatsapp_bindings (
  user_id      uuid PRIMARY KEY REFERENCES users(id),
  number       text NOT NULL UNIQUE CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  verified_at  timestamptz NOT NULL DEFAULT now()
);

-- One pending code per person. The code is kept only as a SHA-256 of
-- number:code, so a code proves the number it was sent to. Five wrong
-- codes lock it; at most p_max_sends codes go out per person per hour.
CREATE TABLE whatsapp_otps (
  user_id       uuid PRIMARY KEY REFERENCES users(id),
  number        text NOT NULL CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  code_hash     bytea NOT NULL CHECK (length(code_hash) = 32),
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  failures      int NOT NULL DEFAULT 0,
  sends         int NOT NULL DEFAULT 1,
  window_start  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON whatsapp_otps (number);

-- Per number: the tenant a bound person is speaking to now, and when the
-- number last wrote (the 24-hour customer service window).
CREATE TABLE whatsapp_contacts (
  number           text PRIMARY KEY CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  current_tenant   uuid REFERENCES tenants(id),
  last_inbound_at  timestamptz,
  updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Inbound message ids already handled: Meta retries deliveries.
CREATE TABLE whatsapp_inbound (
  message_id   text PRIMARY KEY CHECK (length(message_id) BETWEEN 1 AND 256),
  received_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON whatsapp_inbound (received_at);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['whatsapp_bindings', 'whatsapp_otps', 'whatsapp_contacts', 'whatsapp_inbound'] LOOP
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

-- Sends a code: ok, number_taken (bound to someone else) or too_many.
-- A new code replaces the pending one and resets its wrong guesses, but
-- counts toward the hourly sends.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_otp_issue(p_user uuid, p_number text, p_code text, p_ttl interval, p_max_sends int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r whatsapp_otps;
  h bytea := sha256(convert_to(p_number || ':' || p_code, 'UTF8'));
BEGIN
  IF EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = p_number AND b.user_id <> p_user) THEN
    RETURN 'number_taken';
  END IF;
  SELECT * INTO r FROM whatsapp_otps o WHERE o.user_id = p_user FOR UPDATE;
  IF NOT FOUND THEN
    INSERT INTO whatsapp_otps (user_id, number, code_hash, expires_at) VALUES (p_user, p_number, h, now() + p_ttl);
    RETURN 'ok';
  END IF;
  IF r.window_start > now() - interval '1 hour' THEN
    IF r.sends >= p_max_sends THEN
      RETURN 'too_many';
    END IF;
    UPDATE whatsapp_otps o SET number = p_number, code_hash = h, created_at = now(), expires_at = now() + p_ttl, failures = 0, sends = o.sends + 1
     WHERE o.user_id = p_user;
  ELSE
    UPDATE whatsapp_otps o SET number = p_number, code_hash = h, created_at = now(), expires_at = now() + p_ttl, failures = 0, sends = 1, window_start = now()
     WHERE o.user_id = p_user;
  END IF;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- Binds the number once its code is right: none (no code pending),
-- expired, locked, wrong (counts toward the lock), number_taken, or ok
-- with the number. A person has one number: binding replaces theirs.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_otp_verify(p_user uuid, p_code text, p_max_failures int)
RETURNS TABLE (outcome text, number text) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r whatsapp_otps;
  old text;
BEGIN
  SELECT * INTO r FROM whatsapp_otps o WHERE o.user_id = p_user FOR UPDATE;
  IF NOT FOUND THEN
    RETURN QUERY SELECT 'none'::text, NULL::text; RETURN;
  END IF;
  IF r.expires_at <= now() THEN
    RETURN QUERY SELECT 'expired'::text, NULL::text; RETURN;
  END IF;
  IF r.failures >= p_max_failures THEN
    RETURN QUERY SELECT 'locked'::text, NULL::text; RETURN;
  END IF;
  -- Digests of number and code: comparing them in variable time says
  -- nothing useful, and wrong guesses are capped.
  IF r.code_hash <> sha256(convert_to(r.number || ':' || p_code, 'UTF8')) THEN
    UPDATE whatsapp_otps o SET failures = o.failures + 1 WHERE o.user_id = p_user;
    RETURN QUERY SELECT 'wrong'::text, NULL::text; RETURN;
  END IF;
  -- The code is spent either way; the hourly send count is kept.
  UPDATE whatsapp_otps o SET expires_at = now(), failures = p_max_failures WHERE o.user_id = p_user;
  IF EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = r.number AND b.user_id <> p_user) THEN
    RETURN QUERY SELECT 'number_taken'::text, NULL::text; RETURN;
  END IF;
  SELECT b.number INTO old FROM whatsapp_bindings b WHERE b.user_id = p_user;
  DELETE FROM whatsapp_bindings b WHERE b.user_id = p_user;
  INSERT INTO whatsapp_bindings (user_id, number) VALUES (p_user, r.number);
  -- The number's conversation starts afresh, in its new owner's first tenant.
  UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number IN (r.number, old);
  RETURN QUERY SELECT 'ok'::text, r.number;
END
$$;
-- +goose StatementEnd

-- The person a pending code from this number belongs to, when the code
-- arrives as a WhatsApp reply: the code must match one pending for the
-- number. A wrong code counts against every code pending for it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_otp_user_for(p_number text, p_code text)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  u uuid;
BEGIN
  SELECT o.user_id INTO u FROM whatsapp_otps o WHERE o.number = p_number AND o.expires_at > now() AND o.failures < 5
     AND o.code_hash = sha256(convert_to(p_number || ':' || p_code, 'UTF8')) LIMIT 1;
  IF u IS NULL THEN
    UPDATE whatsapp_otps o SET failures = o.failures + 1 WHERE o.number = p_number AND o.expires_at > now();
  END IF;
  RETURN u;
END
$$;
-- +goose StatementEnd

-- A person's binding and pending code, for their account page.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_binding(p_user uuid)
RETURNS TABLE (number text, verified_at timestamptz, pending_number text, pending_expires_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.number, b.verified_at, o.number, o.expires_at
    FROM (SELECT p_user AS user_id) me
    LEFT JOIN whatsapp_bindings b ON b.user_id = me.user_id
    LEFT JOIN whatsapp_otps o ON o.user_id = me.user_id AND o.expires_at > now() AND o.failures < 5
$$;
-- +goose StatementEnd

-- Who a number speaks for, if it is bound to an active person.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_user_of(p_number text)
RETURNS uuid LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.user_id FROM whatsapp_bindings b JOIN users u ON u.id = b.user_id AND u.status = 'active' WHERE b.number = p_number
$$;
-- +goose StatementEnd

-- Removes a person's binding; returns the number it held.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_unbind(p_user uuid)
RETURNS text LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH b AS (DELETE FROM whatsapp_bindings b WHERE b.user_id = p_user RETURNING b.number),
       c AS (UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number IN (SELECT number FROM b))
  SELECT number FROM b
$$;
-- +goose StatementEnd

-- A number's conversation: the tenant it speaks to and when it last wrote.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_contact(p_number text)
RETURNS TABLE (current_tenant uuid, last_inbound_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.current_tenant, c.last_inbound_at FROM whatsapp_contacts c WHERE c.number = p_number
$$;
-- +goose StatementEnd

-- Records that a number wrote (p_inbound) and, with p_set_tenant, the
-- tenant it now speaks to.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_contact_touch(p_number text, p_inbound boolean, p_set_tenant boolean, p_tenant uuid)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO whatsapp_contacts AS c (number, current_tenant, last_inbound_at)
  VALUES (p_number, CASE WHEN p_set_tenant THEN p_tenant END, CASE WHEN p_inbound THEN now() END)
  ON CONFLICT (number) DO UPDATE SET
    current_tenant = CASE WHEN p_set_tenant THEN p_tenant ELSE c.current_tenant END,
    last_inbound_at = CASE WHEN p_inbound THEN now() ELSE c.last_inbound_at END,
    updated_at = now()
$$;
-- +goose StatementEnd

-- Claims an inbound message id: true the first time. Ids older than a
-- week (Meta retries for up to seven days) are forgotten.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_inbound_claim(p_id text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  IF random() < 0.01 THEN
    DELETE FROM whatsapp_inbound i WHERE i.received_at < now() - interval '8 days';
  END IF;
  INSERT INTO whatsapp_inbound (message_id) VALUES (p_id) ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n = 1;
END
$$;
-- +goose StatementEnd

-- Bound numbers of the given people, only for members of a tenant in the
-- caller's scope, with when each last wrote.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_numbers(p_users uuid[])
RETURNS TABLE (user_id uuid, number text, last_inbound_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.user_id, b.number, c.last_inbound_at FROM whatsapp_bindings b
    JOIN users u ON u.id = b.user_id AND u.status = 'active'
    LEFT JOIN whatsapp_contacts c ON c.number = b.number
   WHERE b.user_id = ANY (p_users)
     AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = b.user_id AND m.tenant_id = ANY (taskiem_tenant_scope()))
$$;
-- +goose StatementEnd

-- Tenants with at least one member who has bound a number: those the
-- WhatsApp notifier has work for.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT m.tenant_id FROM memberships m
    JOIN whatsapp_bindings b ON b.user_id = m.user_id
    JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_wa_otp_issue(uuid, text, text, interval, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_otp_verify(uuid, text, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_otp_user_for(text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_binding(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_user_of(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_unbind(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_contact(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_contact_touch(text, boolean, boolean, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_inbound_claim(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_numbers(uuid[]) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_wa_otp_issue(uuid, text, text, interval, int), taskiem_wa_otp_verify(uuid, text, int), taskiem_wa_otp_user_for(text, text),
  taskiem_wa_binding(uuid), taskiem_wa_user_of(text), taskiem_wa_unbind(uuid), taskiem_wa_contact(text), taskiem_wa_contact_touch(text, boolean, boolean, uuid),
  taskiem_wa_inbound_claim(text), taskiem_wa_numbers(uuid[]), taskiem_wa_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_wa_otp_issue(uuid, text, text, interval, int), taskiem_wa_otp_verify(uuid, text, int), taskiem_wa_otp_user_for(text, text),
  taskiem_wa_binding(uuid), taskiem_wa_user_of(text), taskiem_wa_unbind(uuid), taskiem_wa_contact(text), taskiem_wa_contact_touch(text, boolean, boolean, uuid),
  taskiem_wa_inbound_claim(text), taskiem_wa_numbers(uuid[]), taskiem_wa_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_wa_tenants();
DROP FUNCTION taskiem_wa_numbers(uuid[]);
DROP FUNCTION taskiem_wa_inbound_claim(text);
DROP FUNCTION taskiem_wa_contact_touch(text, boolean, boolean, uuid);
DROP FUNCTION taskiem_wa_contact(text);
DROP FUNCTION taskiem_wa_unbind(uuid);
DROP FUNCTION taskiem_wa_user_of(text);
DROP FUNCTION taskiem_wa_binding(uuid);
DROP FUNCTION taskiem_wa_otp_user_for(text, text);
DROP FUNCTION taskiem_wa_otp_verify(uuid, text, int);
DROP FUNCTION taskiem_wa_otp_issue(uuid, text, text, interval, int);
DROP TABLE whatsapp_inbound;
DROP TABLE whatsapp_contacts;
DROP TABLE whatsapp_otps;
DROP TABLE whatsapp_bindings;
