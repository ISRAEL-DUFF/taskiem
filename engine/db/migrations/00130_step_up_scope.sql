-- +goose Up
-- Step-up challenges name what they are for (self-review S35): the
-- operation and its target ("approval.decide run/step/decision",
-- "key.rotate tenant", ...). A step-up assertion is good only for the
-- operation it was asked for. Sign-in and registration challenges have
-- no scope.
ALTER TABLE auth_challenges ADD COLUMN scope text CHECK (scope IS NULL OR length(scope) <= 300);

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_challenge_issue(p_challenge bytea, p_purpose text, p_user uuid, p_ttl interval, p_scope text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM auth_challenges WHERE expires_at < now() - interval '1 hour';
  INSERT INTO auth_challenges (challenge, purpose, user_id, expires_at, scope) VALUES (p_challenge, p_purpose, p_user, now() + p_ttl, p_scope);
$$;
-- +goose StatementEnd

-- Takes a challenge for exactly this scope (NULL for none): it works once,
-- before it expires.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_challenge_take(p_challenge bytea, p_purpose text, p_scope text)
RETURNS TABLE (user_id uuid) LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM auth_challenges c WHERE c.challenge = p_challenge AND c.purpose = p_purpose AND c.expires_at > now()
     AND c.scope IS NOT DISTINCT FROM p_scope
  RETURNING c.user_id;
$$;
-- +goose StatementEnd

-- The unscoped take (replicas not yet upgraded) never takes a scoped
-- challenge.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_challenge_take(p_challenge bytea, p_purpose text)
RETURNS TABLE (user_id uuid) LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM auth_challenges c WHERE c.challenge = p_challenge AND c.purpose = p_purpose AND c.expires_at > now() AND c.scope IS NULL
  RETURNING c.user_id;
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_challenge_issue(bytea, text, uuid, interval, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_challenge_take(bytea, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_challenge_issue(bytea, text, uuid, interval, text), taskiem_auth_challenge_take(bytea, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_challenge_issue(bytea, text, uuid, interval, text), taskiem_auth_challenge_take(bytea, text, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_challenge_take(bytea, text, text);
DROP FUNCTION taskiem_auth_challenge_issue(bytea, text, uuid, interval, text);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_challenge_take(p_challenge bytea, p_purpose text)
RETURNS TABLE (user_id uuid) LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM auth_challenges c WHERE c.challenge = p_challenge AND c.purpose = p_purpose AND c.expires_at > now()
  RETURNING c.user_id;
$$;
-- +goose StatementEnd
ALTER TABLE auth_challenges DROP COLUMN scope;
