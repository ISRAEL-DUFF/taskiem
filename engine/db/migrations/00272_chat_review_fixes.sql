-- Review fixes to chat as the way in (decision 0058).
--
-- A reissued number is a stranger: when the dormant sweep removes a
-- binding, an account made in chat loses the sign-in name derived from
-- that number or Telegram account, so a later sign-up or invitation from
-- it makes a new person instead of finding the old one. A number whose
-- binding is dormant proves nothing to a Telegram sign-up either.
--
-- A sign-up is taken once: the last answer claims its record, so two
-- messages at once (a double tap) make one business.
--
-- A connect link knows the OAuth request it started, by the request's
-- state (hashed), so a connection made on the web under the same name is
-- not taken for the chat's.
--
-- A bound person's message records its activity at most once an hour.
-- +goose Up
CREATE POLICY dispatch_release ON users FOR UPDATE TO taskiem_dispatch
  USING (email LIKE 'owner+%.taskiem.invalid') WITH CHECK (email LIKE 'released+%.taskiem.invalid');
GRANT UPDATE (email) ON users TO taskiem_dispatch;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_bindings_dormant_sweep(p_days int)
RETURNS TABLE (dormant int, removed int) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  d1 int; d2 int; r1 int; r2 int;
  span interval := make_interval(days => p_days);
  gone_users uuid[];
BEGIN
  IF p_days <= 0 THEN
    RETURN QUERY SELECT 0, 0; RETURN;
  END IF;
  UPDATE whatsapp_bindings b SET dormant_at = now() WHERE b.dormant_at IS NULL AND b.last_active_at < now() - span;
  GET DIAGNOSTICS d1 = ROW_COUNT;
  UPDATE telegram_bindings b SET dormant_at = now() WHERE b.dormant_at IS NULL AND b.last_active_at < now() - span;
  GET DIAGNOSTICS d2 = ROW_COUNT;
  WITH gone AS (DELETE FROM whatsapp_bindings b WHERE b.dormant_at < now() - span RETURNING b.number, b.user_id),
  touched AS (UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number IN (SELECT number FROM gone))
  SELECT array_agg(user_id) INTO gone_users FROM gone;
  r1 := coalesce(cardinality(gone_users), 0);
  -- The number's sign-in name goes with it: its next holder is a stranger.
  UPDATE users u SET email = 'released+' || u.id || '@phone.taskiem.invalid'
   WHERE u.id = ANY (coalesce(gone_users, '{}')) AND u.email LIKE 'owner+%@phone.taskiem.invalid';
  WITH gone AS (DELETE FROM telegram_bindings b WHERE b.dormant_at < now() - span RETURNING b.telegram_id, b.user_id),
  touched AS (UPDATE telegram_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.telegram_id IN (SELECT telegram_id FROM gone))
  SELECT array_agg(user_id) INTO gone_users FROM gone;
  r2 := coalesce(cardinality(gone_users), 0);
  UPDATE users u SET email = 'released+' || u.id || '@telegram.taskiem.invalid'
   WHERE u.id = ANY (coalesce(gone_users, '{}')) AND u.email LIKE 'owner+%@telegram.taskiem.invalid';
  RETURN QUERY SELECT d1 + d2, r1 + r2;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_chat_number_owner(p_number text)
RETURNS TABLE (user_id uuid, chat_born boolean, email text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.id, u.password_hash IS NULL AND u.email LIKE '%.taskiem.invalid', u.email
    FROM whatsapp_bindings b JOIN users u ON u.id = b.user_id AND u.status = 'active'
   WHERE b.number = p_number AND b.dormant_at IS NULL
$$;
-- +goose StatementEnd

-- Claims a sign-up's record to make its business: true for the one
-- caller that removed it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_signup_take(p_hash bytea)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  DELETE FROM chat_signups x WHERE x.address_hash = p_hash AND x.expires_at > now();
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n = 1;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_chat_signup_take(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_chat_signup_take(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_chat_signup_take(bytea) TO taskiem_app;

ALTER TABLE connect_links ADD COLUMN oauth_state_hash bytea CHECK (oauth_state_hash IS NULL OR length(oauth_state_hash) = 32);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_binding_touch(p_channel text, p_user uuid, p_days int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  last timestamptz;
  since timestamptz;
BEGIN
  IF p_channel = 'whatsapp' THEN
    SELECT b.last_active_at, b.dormant_at INTO last, since FROM whatsapp_bindings b WHERE b.user_id = p_user;
  ELSIF p_channel = 'telegram' THEN
    SELECT b.last_active_at, b.dormant_at INTO last, since FROM telegram_bindings b WHERE b.user_id = p_user;
  ELSE
    RAISE EXCEPTION 'no such channel' USING ERRCODE = '22023';
  END IF;
  IF NOT FOUND THEN
    RETURN 'none';
  END IF;
  IF since IS NOT NULL AND p_days > 0 THEN
    RETURN 'dormant';
  END IF;
  IF p_days > 0 AND last < now() - make_interval(days => p_days) THEN
    IF p_channel = 'whatsapp' THEN
      UPDATE whatsapp_bindings b SET dormant_at = now() WHERE b.user_id = p_user AND b.dormant_at IS NULL;
    ELSE
      UPDATE telegram_bindings b SET dormant_at = now() WHERE b.user_id = p_user AND b.dormant_at IS NULL;
    END IF;
    RETURN 'dormant';
  END IF;
  -- Activity is what dormancy counts in days: an hour's precision is
  -- plenty, and spares a write per message.
  IF last < now() - interval '1 hour' THEN
    IF p_channel = 'whatsapp' THEN
      UPDATE whatsapp_bindings b SET last_active_at = now() WHERE b.user_id = p_user AND b.dormant_at IS NULL;
    ELSE
      UPDATE telegram_bindings b SET last_active_at = now() WHERE b.user_id = p_user AND b.dormant_at IS NULL;
    END IF;
  END IF;
  RETURN 'active';
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_binding_touch(p_channel text, p_user uuid, p_days int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  last timestamptz;
  since timestamptz;
BEGIN
  IF p_channel = 'whatsapp' THEN
    SELECT b.last_active_at, b.dormant_at INTO last, since FROM whatsapp_bindings b WHERE b.user_id = p_user FOR UPDATE;
  ELSIF p_channel = 'telegram' THEN
    SELECT b.last_active_at, b.dormant_at INTO last, since FROM telegram_bindings b WHERE b.user_id = p_user FOR UPDATE;
  ELSE
    RAISE EXCEPTION 'no such channel' USING ERRCODE = '22023';
  END IF;
  IF NOT FOUND THEN
    RETURN 'none';
  END IF;
  IF since IS NOT NULL AND p_days > 0 THEN
    RETURN 'dormant';
  END IF;
  IF p_days > 0 AND last < now() - make_interval(days => p_days) THEN
    IF p_channel = 'whatsapp' THEN
      UPDATE whatsapp_bindings b SET dormant_at = now() WHERE b.user_id = p_user;
    ELSE
      UPDATE telegram_bindings b SET dormant_at = now() WHERE b.user_id = p_user;
    END IF;
    RETURN 'dormant';
  END IF;
  IF p_channel = 'whatsapp' THEN
    UPDATE whatsapp_bindings b SET last_active_at = now(), dormant_at = NULL WHERE b.user_id = p_user;
  ELSE
    UPDATE telegram_bindings b SET last_active_at = now(), dormant_at = NULL WHERE b.user_id = p_user;
  END IF;
  RETURN 'active';
END
$$;
-- +goose StatementEnd
ALTER TABLE connect_links DROP COLUMN oauth_state_hash;
DROP FUNCTION taskiem_chat_signup_take(bytea);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_chat_number_owner(p_number text)
RETURNS TABLE (user_id uuid, chat_born boolean, email text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.id, u.password_hash IS NULL AND u.email LIKE '%.taskiem.invalid', u.email
    FROM whatsapp_bindings b JOIN users u ON u.id = b.user_id AND u.status = 'active'
   WHERE b.number = p_number
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_bindings_dormant_sweep(p_days int)
RETURNS TABLE (dormant int, removed int) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  d1 int; d2 int; r1 int; r2 int;
  span interval := make_interval(days => p_days);
BEGIN
  IF p_days <= 0 THEN
    RETURN QUERY SELECT 0, 0; RETURN;
  END IF;
  UPDATE whatsapp_bindings b SET dormant_at = now() WHERE b.dormant_at IS NULL AND b.last_active_at < now() - span;
  GET DIAGNOSTICS d1 = ROW_COUNT;
  UPDATE telegram_bindings b SET dormant_at = now() WHERE b.dormant_at IS NULL AND b.last_active_at < now() - span;
  GET DIAGNOSTICS d2 = ROW_COUNT;
  WITH gone AS (DELETE FROM whatsapp_bindings b WHERE b.dormant_at < now() - span RETURNING b.number)
  UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number IN (SELECT number FROM gone);
  GET DIAGNOSTICS r1 = ROW_COUNT;
  WITH gone AS (DELETE FROM telegram_bindings b WHERE b.dormant_at < now() - span RETURNING b.telegram_id)
  UPDATE telegram_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.telegram_id IN (SELECT telegram_id FROM gone);
  GET DIAGNOSTICS r2 = ROW_COUNT;
  RETURN QUERY SELECT d1 + d2, r1 + r2;
END
$$;
-- +goose StatementEnd
REVOKE UPDATE (email) ON users FROM taskiem_dispatch;
DROP POLICY dispatch_release ON users;
