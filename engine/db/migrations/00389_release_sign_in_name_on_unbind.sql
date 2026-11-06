-- A reissued number is a stranger however its binding ends (F18). The
-- dormant sweep (00272) releases the sign-in name an account made in chat
-- has from its number or Telegram account, so the next holder signing up
-- or answering an invitation is a new person. The web unlink, and linking
-- another number or Telegram account in its place, kept that name, so the
-- old number's next holder was found as, and bound to, the old account.
-- Now each of them releases it too: the account becomes
-- released+<id>@phone.taskiem.invalid (or @telegram), as the sweep makes
-- it. The functions stay owned by taskiem_dispatch, which the
-- dispatch_release policy and the users.email grant of 00272 allow.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_unbind(p_user uuid)
RETURNS text LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH b AS (DELETE FROM whatsapp_bindings b WHERE b.user_id = p_user RETURNING b.number),
       c AS (UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number IN (SELECT number FROM b)),
       r AS (UPDATE users u SET email = 'released+' || u.id || '@phone.taskiem.invalid'
              WHERE u.id = p_user AND EXISTS (SELECT 1 FROM b) AND u.email LIKE 'owner+%@phone.taskiem.invalid')
  SELECT number FROM b
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_otp_verify(p_user uuid, p_code text, p_max_failures int)
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
  -- The old number's sign-in name goes with it: its next holder is a
  -- stranger.
  IF old IS NOT NULL AND old <> r.number THEN
    UPDATE users u SET email = 'released+' || u.id || '@phone.taskiem.invalid'
     WHERE u.id = p_user AND u.email LIKE 'owner+%@phone.taskiem.invalid';
  END IF;
  -- The number's conversation starts afresh, in its new owner's first tenant.
  UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number IN (r.number, old);
  RETURN QUERY SELECT 'ok'::text, r.number;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_tg_unbind(p_user uuid)
RETURNS bigint LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH b AS (DELETE FROM telegram_bindings b WHERE b.user_id = p_user RETURNING b.telegram_id),
       c AS (UPDATE telegram_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.telegram_id IN (SELECT telegram_id FROM b)),
       r AS (UPDATE users u SET email = 'released+' || u.id || '@telegram.taskiem.invalid'
              WHERE u.id = p_user AND EXISTS (SELECT 1 FROM b) AND u.email LIKE 'owner+%@telegram.taskiem.invalid')
  SELECT telegram_id FROM b
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_tg_code_redeem(p_code_hash bytea, p_telegram_id bigint, p_username text)
RETURNS TABLE (outcome text, user_id uuid) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  u uuid;
  old bigint;
BEGIN
  DELETE FROM telegram_link_codes c WHERE c.code_hash = p_code_hash AND c.expires_at > now() RETURNING c.user_id INTO u;
  IF u IS NULL OR NOT EXISTS (SELECT 1 FROM users x WHERE x.id = u AND x.status = 'active') THEN
    RETURN QUERY SELECT 'invalid'::text, NULL::uuid; RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM telegram_bindings b WHERE b.telegram_id = p_telegram_id AND b.user_id <> u) THEN
    RETURN QUERY SELECT 'account_taken'::text, NULL::uuid; RETURN;
  END IF;
  SELECT b.telegram_id INTO old FROM telegram_bindings b WHERE b.user_id = u;
  DELETE FROM telegram_bindings b WHERE b.user_id = u;
  INSERT INTO telegram_bindings (user_id, telegram_id, username) VALUES (u, p_telegram_id, NULLIF(p_username, ''));
  -- The old account's sign-in name goes with it.
  IF old IS NOT NULL AND old <> p_telegram_id THEN
    UPDATE users x SET email = 'released+' || x.id || '@telegram.taskiem.invalid'
     WHERE x.id = u AND x.email LIKE 'owner+%@telegram.taskiem.invalid';
  END IF;
  UPDATE telegram_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.telegram_id IN (p_telegram_id, old);
  RETURN QUERY SELECT 'ok'::text, u;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_wa_unbind(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_wa_otp_verify(uuid, text, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_unbind(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tg_code_redeem(bytea, bigint, text) OWNER TO taskiem_dispatch;

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_unbind(p_user uuid)
RETURNS text LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH b AS (DELETE FROM whatsapp_bindings b WHERE b.user_id = p_user RETURNING b.number),
       c AS (UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number IN (SELECT number FROM b))
  SELECT number FROM b
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_otp_verify(p_user uuid, p_code text, p_max_failures int)
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
  IF r.code_hash <> sha256(convert_to(r.number || ':' || p_code, 'UTF8')) THEN
    UPDATE whatsapp_otps o SET failures = o.failures + 1 WHERE o.user_id = p_user;
    RETURN QUERY SELECT 'wrong'::text, NULL::text; RETURN;
  END IF;
  UPDATE whatsapp_otps o SET expires_at = now(), failures = p_max_failures WHERE o.user_id = p_user;
  IF EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = r.number AND b.user_id <> p_user) THEN
    RETURN QUERY SELECT 'number_taken'::text, NULL::text; RETURN;
  END IF;
  SELECT b.number INTO old FROM whatsapp_bindings b WHERE b.user_id = p_user;
  DELETE FROM whatsapp_bindings b WHERE b.user_id = p_user;
  INSERT INTO whatsapp_bindings (user_id, number) VALUES (p_user, r.number);
  UPDATE whatsapp_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.number IN (r.number, old);
  RETURN QUERY SELECT 'ok'::text, r.number;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_tg_unbind(p_user uuid)
RETURNS bigint LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH b AS (DELETE FROM telegram_bindings b WHERE b.user_id = p_user RETURNING b.telegram_id),
       c AS (UPDATE telegram_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.telegram_id IN (SELECT telegram_id FROM b))
  SELECT telegram_id FROM b
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_tg_code_redeem(p_code_hash bytea, p_telegram_id bigint, p_username text)
RETURNS TABLE (outcome text, user_id uuid) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  u uuid;
  old bigint;
BEGIN
  DELETE FROM telegram_link_codes c WHERE c.code_hash = p_code_hash AND c.expires_at > now() RETURNING c.user_id INTO u;
  IF u IS NULL OR NOT EXISTS (SELECT 1 FROM users x WHERE x.id = u AND x.status = 'active') THEN
    RETURN QUERY SELECT 'invalid'::text, NULL::uuid; RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM telegram_bindings b WHERE b.telegram_id = p_telegram_id AND b.user_id <> u) THEN
    RETURN QUERY SELECT 'account_taken'::text, NULL::uuid; RETURN;
  END IF;
  SELECT b.telegram_id INTO old FROM telegram_bindings b WHERE b.user_id = u;
  DELETE FROM telegram_bindings b WHERE b.user_id = u;
  INSERT INTO telegram_bindings (user_id, telegram_id, username) VALUES (u, p_telegram_id, NULLIF(p_username, ''));
  UPDATE telegram_contacts c SET current_tenant = NULL, updated_at = now() WHERE c.telegram_id IN (p_telegram_id, old);
  RETURN QUERY SELECT 'ok'::text, u;
END
$$;
-- +goose StatementEnd
