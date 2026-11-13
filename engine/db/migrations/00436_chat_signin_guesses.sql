-- Guessing a chat sign-in code (docs/spec/chat-sign-in.md 7). Five wrong
-- tries locked a code, but each new code started the count again, so a
-- number's codes could be guessed 25 times an hour for ever. Wrong tries
-- are now also counted across codes (strikes): every fifth locks the
-- person's codes, for 15 minutes the first time and twice as long each
-- time after, up to a day. A right code, or a week with no wrong try,
-- clears them. While locked no code is issued and none is accepted. A
-- wrong or locked try now answers with the person, so it can be audited
-- (auth.chat_signin_refused).
-- +goose Up
ALTER TABLE chat_signin_codes ADD COLUMN strikes int NOT NULL DEFAULT 0,
  ADD COLUMN strike_at timestamptz,
  ADD COLUMN locked_until timestamptz;

-- Issues a code: ok, too_many, or locked. A new code replaces the pending
-- one and its wrong tries, and counts toward the hour's sends; the
-- strikes stay.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_chat_code_issue(p_user uuid, p_number text, p_channel text, p_code text, p_ttl interval, p_max_sends int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r chat_signin_codes;
  h bytea := sha256(convert_to(p_user::text || ':' || p_number || ':' || p_code, 'UTF8'));
BEGIN
  SELECT * INTO r FROM chat_signin_codes c WHERE c.user_id = p_user FOR UPDATE;
  IF NOT FOUND THEN
    INSERT INTO chat_signin_codes (user_id, number, channel, code_hash, expires_at) VALUES (p_user, p_number, p_channel, h, now() + p_ttl);
    RETURN 'ok';
  END IF;
  IF r.locked_until > now() THEN
    RETURN 'locked';
  END IF;
  IF r.strike_at < now() - interval '7 days' THEN
    UPDATE chat_signin_codes c SET strikes = 0, strike_at = NULL, locked_until = NULL WHERE c.user_id = p_user;
  END IF;
  IF r.window_start > now() - interval '1 hour' THEN
    IF r.sends >= p_max_sends THEN
      RETURN 'too_many';
    END IF;
    UPDATE chat_signin_codes c SET number = p_number, channel = p_channel, code_hash = h, created_at = now(), expires_at = now() + p_ttl,
      failures = 0, sends = c.sends + 1 WHERE c.user_id = p_user;
  ELSE
    UPDATE chat_signin_codes c SET number = p_number, channel = p_channel, code_hash = h, created_at = now(), expires_at = now() + p_ttl,
      failures = 0, sends = 1, window_start = now() WHERE c.user_id = p_user;
  END IF;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- Checks a code typed for a number: ok with the person (the code is spent
-- and the strikes cleared), none, expired, or locked or wrong with the
-- person (a wrong try counts toward both locks).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_chat_code_check(p_number text, p_code text, p_max_failures int)
RETURNS TABLE (outcome text, user_id uuid, channel text) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r chat_signin_codes;
  n int;
BEGIN
  SELECT * INTO r FROM chat_signin_codes c WHERE c.number = p_number ORDER BY c.created_at DESC LIMIT 1 FOR UPDATE;
  IF NOT FOUND THEN
    RETURN QUERY SELECT 'none'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  IF r.expires_at <= now() THEN
    RETURN QUERY SELECT 'expired'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  IF r.failures >= p_max_failures OR r.locked_until > now() THEN
    RETURN QUERY SELECT 'locked'::text, r.user_id, r.channel; RETURN;
  END IF;
  IF r.code_hash <> sha256(convert_to(r.user_id::text || ':' || r.number || ':' || p_code, 'UTF8')) THEN
    n := r.strikes + 1;
    UPDATE chat_signin_codes c SET failures = c.failures + 1, strikes = n, strike_at = now(),
      locked_until = CASE WHEN n % p_max_failures = 0
        THEN now() + LEAST(interval '15 minutes' * power(2, LEAST(n / p_max_failures - 1, 10)), interval '1 day')
        ELSE c.locked_until END
     WHERE c.user_id = r.user_id;
    RETURN QUERY SELECT 'wrong'::text, r.user_id, r.channel; RETURN;
  END IF;
  -- Spent; the hour's sends are kept.
  UPDATE chat_signin_codes c SET expires_at = now(), failures = p_max_failures, strikes = 0, strike_at = NULL, locked_until = NULL WHERE c.user_id = r.user_id;
  RETURN QUERY SELECT 'ok'::text, r.user_id, r.channel;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_chat_code_issue(p_user uuid, p_number text, p_channel text, p_code text, p_ttl interval, p_max_sends int)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r chat_signin_codes;
  h bytea := sha256(convert_to(p_user::text || ':' || p_number || ':' || p_code, 'UTF8'));
BEGIN
  SELECT * INTO r FROM chat_signin_codes c WHERE c.user_id = p_user FOR UPDATE;
  IF NOT FOUND THEN
    INSERT INTO chat_signin_codes (user_id, number, channel, code_hash, expires_at) VALUES (p_user, p_number, p_channel, h, now() + p_ttl);
    RETURN 'ok';
  END IF;
  IF r.window_start > now() - interval '1 hour' THEN
    IF r.sends >= p_max_sends THEN
      RETURN 'too_many';
    END IF;
    UPDATE chat_signin_codes c SET number = p_number, channel = p_channel, code_hash = h, created_at = now(), expires_at = now() + p_ttl,
      failures = 0, sends = c.sends + 1 WHERE c.user_id = p_user;
  ELSE
    UPDATE chat_signin_codes c SET number = p_number, channel = p_channel, code_hash = h, created_at = now(), expires_at = now() + p_ttl,
      failures = 0, sends = 1, window_start = now() WHERE c.user_id = p_user;
  END IF;
  RETURN 'ok';
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_chat_code_check(p_number text, p_code text, p_max_failures int)
RETURNS TABLE (outcome text, user_id uuid, channel text) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r chat_signin_codes;
BEGIN
  SELECT * INTO r FROM chat_signin_codes c WHERE c.number = p_number ORDER BY c.created_at DESC LIMIT 1 FOR UPDATE;
  IF NOT FOUND THEN
    RETURN QUERY SELECT 'none'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  IF r.expires_at <= now() THEN
    RETURN QUERY SELECT 'expired'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  IF r.failures >= p_max_failures THEN
    RETURN QUERY SELECT 'locked'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  IF r.code_hash <> sha256(convert_to(r.user_id::text || ':' || r.number || ':' || p_code, 'UTF8')) THEN
    UPDATE chat_signin_codes c SET failures = c.failures + 1 WHERE c.user_id = r.user_id;
    RETURN QUERY SELECT 'wrong'::text, NULL::uuid, NULL::text; RETURN;
  END IF;
  UPDATE chat_signin_codes c SET expires_at = now(), failures = p_max_failures WHERE c.user_id = r.user_id;
  RETURN QUERY SELECT 'ok'::text, r.user_id, r.channel;
END
$$;
-- +goose StatementEnd
ALTER TABLE chat_signin_codes DROP COLUMN locked_until, DROP COLUMN strike_at, DROP COLUMN strikes;
