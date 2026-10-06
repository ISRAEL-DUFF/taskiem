-- Passkeys (WebAuthn) for sign-in and step-up (spec 13.2).

-- +goose Up
-- Credentials belong to people (users are global), not to one tenant.
CREATE TABLE webauthn_credentials (
  id              bytea PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 1023),
  user_id         uuid NOT NULL REFERENCES users(id),
  public_key      bytea NOT NULL,             -- COSE_Key
  alg             int NOT NULL,
  sign_count      bigint NOT NULL DEFAULT 0,
  aaguid          bytea,
  backup_eligible boolean NOT NULL DEFAULT false,
  name            text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_used_at    timestamptz
);
CREATE INDEX ON webauthn_credentials (user_id);

ALTER TABLE webauthn_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE webauthn_credentials FORCE ROW LEVEL SECURITY;
-- Visible, like the user, to the tenants the user belongs to.
CREATE POLICY tenant_isolation ON webauthn_credentials TO taskiem_app
  USING (EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = webauthn_credentials.user_id AND m.tenant_id = ANY (taskiem_tenant_scope())))
  WITH CHECK (EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = webauthn_credentials.user_id AND m.tenant_id = ANY (taskiem_tenant_scope())));
GRANT SELECT, INSERT, DELETE ON webauthn_credentials TO taskiem_app;
GRANT UPDATE (name) ON webauthn_credentials TO taskiem_app;
CREATE POLICY dispatch ON webauthn_credentials TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (id, user_id, public_key, alg, sign_count), UPDATE (sign_count, last_used_at) ON webauthn_credentials TO taskiem_dispatch;

-- Single-use challenges. Sign-in challenges exist before anyone is known,
-- so the table is reached only through the functions below.
CREATE TABLE auth_challenges (
  challenge  bytea PRIMARY KEY CHECK (length(challenge) = 32),
  purpose    text NOT NULL CHECK (purpose IN ('register', 'login', 'step_up')),
  user_id    uuid,
  expires_at timestamptz NOT NULL
);
ALTER TABLE auth_challenges ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_challenges FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON auth_challenges TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON auth_challenges TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON auth_challenges FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON auth_challenges TO taskiem_dispatch;

-- How a session was signed in to, for policies that ask for passkeys.
ALTER TABLE sessions ADD COLUMN auth_method text NOT NULL DEFAULT 'password' CHECK (auth_method IN ('password', 'passkey', 'sso'));
GRANT SELECT (auth_method) ON sessions TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_challenge_issue(p_challenge bytea, p_purpose text, p_user uuid, p_ttl interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM auth_challenges WHERE expires_at < now() - interval '1 hour';
  INSERT INTO auth_challenges (challenge, purpose, user_id, expires_at) VALUES (p_challenge, p_purpose, p_user, now() + p_ttl);
$$;
-- +goose StatementEnd

-- Takes a challenge: it works once, before it expires.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_challenge_take(p_challenge bytea, p_purpose text)
RETURNS TABLE (user_id uuid) LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM auth_challenges c WHERE c.challenge = p_challenge AND c.purpose = p_purpose AND c.expires_at > now()
  RETURNING c.user_id;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_find_passkey(p_id bytea)
RETURNS TABLE (user_id uuid, public_key bytea, alg int, sign_count bigint, status text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.user_id, c.public_key, c.alg, c.sign_count, u.status FROM webauthn_credentials c JOIN users u ON u.id = c.user_id WHERE c.id = p_id
$$;
-- +goose StatementEnd

-- Records a verified use; refuses a counter that did not advance, so two
-- sign-ins racing with one assertion cannot both pass.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_passkey_used(p_id bytea, p_old bigint, p_new bigint)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH u AS (UPDATE webauthn_credentials SET sign_count = p_new, last_used_at = now()
              WHERE id = p_id AND sign_count = p_old RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM u)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_has_passkey(p_user uuid)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM webauthn_credentials WHERE user_id = p_user)
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_auth_session(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_session(p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid, auth_method text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, s.tenant_id, s.auth_method FROM sessions s
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_session(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_challenge_issue(bytea, text, uuid, interval) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_challenge_take(bytea, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_find_passkey(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_passkey_used(bytea, bigint, bigint) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_has_passkey(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_session(bytea), taskiem_auth_challenge_issue(bytea, text, uuid, interval), taskiem_auth_challenge_take(bytea, text),
  taskiem_auth_find_passkey(bytea), taskiem_auth_passkey_used(bytea, bigint, bigint), taskiem_auth_has_passkey(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_session(bytea), taskiem_auth_challenge_issue(bytea, text, uuid, interval), taskiem_auth_challenge_take(bytea, text),
  taskiem_auth_find_passkey(bytea), taskiem_auth_passkey_used(bytea, bigint, bigint), taskiem_auth_has_passkey(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_session(bytea);
DROP FUNCTION taskiem_auth_has_passkey(uuid);
DROP FUNCTION taskiem_auth_passkey_used(bytea, bigint, bigint);
DROP FUNCTION taskiem_auth_find_passkey(bytea);
DROP FUNCTION taskiem_auth_challenge_take(bytea, text);
DROP FUNCTION taskiem_auth_challenge_issue(bytea, text, uuid, interval);
ALTER TABLE sessions DROP COLUMN auth_method;
DROP TABLE auth_challenges;
DROP TABLE webauthn_credentials;
