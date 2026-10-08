-- Operator identity for the operator console (decision 0027,
-- docs/operator-console.md). Operators are Taskiem's own staff: a
-- platform-level identity, not a tenant role. Accounts are made by the
-- operator CLI only; an operator enrols a passkey from a one-time link the
-- CLI prints, signs in with it (or with single sign-on from a configured
-- issuer), and proves the passkey again for every write. Sessions are
-- their own table and token, never a tenant session.
--
-- No application code reads these tables directly: everything goes
-- through the definer functions below, owned by taskiem_dispatch, as for
-- sign-in challenges (00021) and catalogue reviewers (00106).
--
-- The platform audit chain: operator actions are appended to the ordinary
-- hash chain (audit_log, taskiem_audit_append) under a reserved chain id
-- that no tenant can ever have, so the anchoring job signs and delivers it
-- like any tenant's chain, and exports verify with `taskiem audit verify`.

-- +goose Up
ALTER TABLE tenants ADD CONSTRAINT tenants_not_platform_chain CHECK (id <> 'ffffffff-ffff-ffff-ffff-ffffffffffff');
-- Anchors of the platform chain name no tenant. Tenants are never
-- deleted, and anchors are written only for chain heads that exist.
ALTER TABLE audit_anchors DROP CONSTRAINT audit_anchors_tenant_id_fkey;

CREATE TABLE operators (
  id           uuid PRIMARY KEY,
  email        text NOT NULL UNIQUE CHECK (email = lower(email) AND email ~ '^[^@\s]+@[^@\s]+$' AND length(email) <= 254),
  name         text NOT NULL DEFAULT '' CHECK (length(name) <= 200),
  status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  -- Single sign-on: the issuer and subject pinned at the first sign-in.
  oidc_issuer  text,
  oidc_subject text,
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  disabled_by  text,
  disabled_at  timestamptz,
  UNIQUE (oidc_issuer, oidc_subject)
);

CREATE TABLE operator_credentials (
  id              bytea PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 1023),
  operator_id     uuid NOT NULL REFERENCES operators(id),
  public_key      bytea NOT NULL,
  alg             int NOT NULL,
  sign_count      bigint NOT NULL DEFAULT 0,
  aaguid          bytea,
  backup_eligible boolean NOT NULL DEFAULT false,
  name            text NOT NULL DEFAULT '' CHECK (length(name) <= 64),
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_used_at    timestamptz
);
CREATE INDEX operator_credentials_operator ON operator_credentials (operator_id);

-- One-time enrolment links (taskiem operators add|enrol). Kept hashed.
CREATE TABLE operator_enrolments (
  token_hash  bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  operator_id uuid NOT NULL REFERENCES operators(id),
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz
);

-- Single-use WebAuthn challenges, apart from tenants' (auth_challenges):
-- a challenge issued to one side can never be answered on the other.
CREATE TABLE operator_challenges (
  challenge   bytea PRIMARY KEY CHECK (length(challenge) = 32),
  purpose     text NOT NULL CHECK (purpose IN ('enrol', 'login', 'step_up')),
  operator_id uuid,
  scope       text CHECK (scope IS NULL OR length(scope) <= 300),
  expires_at  timestamptz NOT NULL
);

CREATE TABLE operator_sessions (
  token_hash  bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  operator_id uuid NOT NULL REFERENCES operators(id),
  auth_method text NOT NULL CHECK (auth_method IN ('passkey', 'sso')),
  ip          text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  revoked_at  timestamptz,
  CHECK (expires_at <= created_at + interval '8 hours')
);
CREATE INDEX operator_sessions_operator ON operator_sessions (operator_id);

-- Single sign-on requests in flight (state, nonce, PKCE verifier, and the
-- hash of the cookie binding the browser that started it).
CREATE TABLE operator_sso_requests (
  state      bytea PRIMARY KEY CHECK (length(state) = 32),
  nonce      text NOT NULL,
  verifier   text NOT NULL,
  binding    bytea NOT NULL CHECK (length(binding) = 32),
  expires_at timestamptz NOT NULL
);

ALTER TABLE operators ENABLE ROW LEVEL SECURITY;
ALTER TABLE operators FORCE ROW LEVEL SECURITY;
ALTER TABLE operator_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_credentials FORCE ROW LEVEL SECURITY;
ALTER TABLE operator_enrolments ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_enrolments FORCE ROW LEVEL SECURITY;
ALTER TABLE operator_challenges ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_challenges FORCE ROW LEVEL SECURITY;
ALTER TABLE operator_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_sessions FORCE ROW LEVEL SECURITY;
ALTER TABLE operator_sso_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE operator_sso_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON operators TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY no_direct_access ON operator_credentials TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY no_direct_access ON operator_enrolments TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY no_direct_access ON operator_challenges TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY no_direct_access ON operator_sessions TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY no_direct_access ON operator_sso_requests TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON operators TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON operator_credentials TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON operator_enrolments TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON operator_challenges TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON operator_sessions TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON operator_sso_requests TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON operators, operator_credentials, operator_enrolments, operator_challenges, operator_sessions, operator_sso_requests FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE ON operators, operator_credentials, operator_enrolments, operator_sessions TO taskiem_dispatch;
GRANT SELECT, INSERT, DELETE ON operator_challenges, operator_sso_requests TO taskiem_dispatch;

-- Accounts (the CLI). Adding an existing email reactivates it; disabling
-- one ends its sessions and unused enrolment links at once.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_operator_add(p_email text, p_name text, p_by text)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id uuid;
BEGIN
  INSERT INTO operators (id, email, name, created_by) VALUES (gen_random_uuid(), lower(trim(p_email)), COALESCE(p_name, ''), p_by)
  ON CONFLICT (email) DO UPDATE SET status = 'active', disabled_by = NULL, disabled_at = NULL,
     name = CASE WHEN EXCLUDED.name = '' THEN operators.name ELSE EXCLUDED.name END
  RETURNING id INTO v_id;
  RETURN v_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_operator_disable(p_email text, p_by text)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id uuid;
BEGIN
  UPDATE operators SET status = 'disabled', disabled_by = p_by, disabled_at = now() WHERE email = lower(trim(p_email)) RETURNING id INTO v_id;
  IF v_id IS NULL THEN
    RAISE EXCEPTION 'no operator %', p_email USING ERRCODE = 'P0002';
  END IF;
  UPDATE operator_sessions SET revoked_at = now() WHERE operator_id = v_id AND revoked_at IS NULL;
  UPDATE operator_enrolments SET used_at = now() WHERE operator_id = v_id AND used_at IS NULL;
  RETURN v_id;
END
$$;
-- +goose StatementEnd

-- Removes an operator's passkeys (a lost device) and ends their sessions;
-- they enrol again from a new link.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_operator_reset(p_email text)
RETURNS bigint LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id uuid;
  v_n bigint;
BEGIN
  SELECT id INTO v_id FROM operators WHERE email = lower(trim(p_email));
  IF v_id IS NULL THEN
    RAISE EXCEPTION 'no operator %', p_email USING ERRCODE = 'P0002';
  END IF;
  -- Credentials are kept for the record but can no longer sign in.
  UPDATE operator_credentials SET public_key = '\x', alg = 0 WHERE operator_id = v_id AND alg <> 0;
  GET DIAGNOSTICS v_n = ROW_COUNT;
  UPDATE operator_sessions SET revoked_at = now() WHERE operator_id = v_id AND revoked_at IS NULL;
  RETURN v_n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_operators()
RETURNS TABLE (id uuid, email text, name text, status text, passkeys bigint, sso boolean, created_by text, created_at timestamptz, last_sign_in timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.id, o.email, o.name, o.status,
         (SELECT count(*) FROM operator_credentials c WHERE c.operator_id = o.id AND c.alg <> 0),
         o.oidc_subject IS NOT NULL, o.created_by, o.created_at,
         (SELECT max(s.created_at) FROM operator_sessions s WHERE s.operator_id = o.id)
    FROM operators o ORDER BY o.email
$$;
-- +goose StatementEnd

-- Enrolment: a link for an active operator, good once until it expires.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_enrol_issue(p_email text, p_token_hash bytea, p_ttl interval, p_by text)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id uuid;
BEGIN
  SELECT id INTO v_id FROM operators WHERE email = lower(trim(p_email)) AND status = 'active';
  IF v_id IS NULL THEN
    RAISE EXCEPTION 'no active operator %', p_email USING ERRCODE = 'P0002';
  END IF;
  IF p_ttl > interval '7 days' THEN
    RAISE EXCEPTION 'an enrolment link lasts at most 7 days' USING ERRCODE = '22023';
  END IF;
  INSERT INTO operator_enrolments (token_hash, operator_id, created_by, expires_at) VALUES (p_token_hash, v_id, p_by, now() + p_ttl);
  RETURN v_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_enrol_check(p_token_hash bytea)
RETURNS TABLE (operator_id uuid, email text, name text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.id, o.email, o.name FROM operator_enrolments e JOIN operators o ON o.id = e.operator_id
   WHERE e.token_hash = p_token_hash AND e.used_at IS NULL AND e.expires_at > now() AND o.status = 'active'
$$;
-- +goose StatementEnd

-- Spends the link and stores the passkey in one step.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_enrol_complete(p_token_hash bytea, p_operator uuid, p_id bytea, p_public_key bytea, p_alg int,
                                           p_sign_count bigint, p_aaguid bytea, p_backup boolean, p_name text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  UPDATE operator_enrolments e SET used_at = now()
   WHERE e.token_hash = p_token_hash AND e.operator_id = p_operator AND e.used_at IS NULL AND e.expires_at > now()
     AND EXISTS (SELECT 1 FROM operators o WHERE o.id = p_operator AND o.status = 'active');
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  INSERT INTO operator_credentials (id, operator_id, public_key, alg, sign_count, aaguid, backup_eligible, name)
  VALUES (p_id, p_operator, p_public_key, p_alg, p_sign_count, p_aaguid, p_backup, p_name);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_credential_ids(p_operator uuid)
RETURNS SETOF bytea LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT id FROM operator_credentials WHERE operator_id = p_operator AND alg <> 0
$$;
-- +goose StatementEnd

-- Challenges: once, before expiry, for the purpose and scope issued.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_challenge_issue(p_challenge bytea, p_purpose text, p_operator uuid, p_ttl interval, p_scope text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM operator_challenges WHERE expires_at < now() - interval '1 hour';
  INSERT INTO operator_challenges (challenge, purpose, operator_id, expires_at, scope) VALUES (p_challenge, p_purpose, p_operator, now() + p_ttl, p_scope);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_challenge_take(p_challenge bytea, p_purpose text, p_scope text)
RETURNS TABLE (operator_id uuid) LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM operator_challenges c WHERE c.challenge = p_challenge AND c.purpose = p_purpose AND c.expires_at > now()
     AND c.scope IS NOT DISTINCT FROM p_scope
  RETURNING c.operator_id;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_credential_find(p_id bytea)
RETURNS TABLE (operator_id uuid, public_key bytea, alg int, sign_count bigint, status text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.operator_id, c.public_key, c.alg, c.sign_count, o.status FROM operator_credentials c JOIN operators o ON o.id = c.operator_id
   WHERE c.id = p_id AND c.alg <> 0
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_credential_used(p_id bytea, p_old bigint, p_new bigint)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH u AS (UPDATE operator_credentials SET sign_count = p_new, last_used_at = now()
              WHERE id = p_id AND sign_count = p_old RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM u)
$$;
-- +goose StatementEnd

-- Sessions: short (at most 8 hours, by the CHECK), for active operators.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_session_start(p_token_hash bytea, p_operator uuid, p_method text, p_ttl interval, p_ip text)
RETURNS void LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM operators WHERE id = p_operator AND status = 'active') THEN
    RAISE EXCEPTION 'operator % is not active', p_operator USING ERRCODE = '42501';
  END IF;
  INSERT INTO operator_sessions (token_hash, operator_id, auth_method, ip, expires_at) VALUES (p_token_hash, p_operator, p_method, COALESCE(p_ip, ''), now() + p_ttl);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_session(p_token_hash bytea)
RETURNS TABLE (operator_id uuid, email text, name text, auth_method text, expires_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.id, o.email, o.name, s.auth_method, s.expires_at FROM operator_sessions s JOIN operators o ON o.id = s.operator_id
   WHERE s.token_hash = p_token_hash AND s.revoked_at IS NULL AND s.expires_at > now() AND o.status = 'active'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_session_end(p_token_hash bytea)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE operator_sessions SET revoked_at = now() WHERE token_hash = p_token_hash AND revoked_at IS NULL;
$$;
-- +goose StatementEnd

-- Single sign-on: requests in flight, and the operator an identity
-- provider's assertion names. Only an existing, active operator signs in
-- (no accounts made by sign-in); the first sign-in pins the issuer and
-- subject, and later ones must match them.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_sso_begin(p_state bytea, p_nonce text, p_verifier text, p_binding bytea, p_ttl interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM operator_sso_requests WHERE expires_at < now();
  INSERT INTO operator_sso_requests (state, nonce, verifier, binding, expires_at) VALUES (p_state, p_nonce, p_verifier, p_binding, now() + p_ttl);
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_sso_take(p_state bytea)
RETURNS TABLE (nonce text, verifier text, binding bytea)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM operator_sso_requests r WHERE r.state = p_state AND r.expires_at > now()
  RETURNING r.nonce, r.verifier, r.binding;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_sso_operator(p_issuer text, p_subject text, p_email text)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  o operators%ROWTYPE;
BEGIN
  SELECT * INTO o FROM operators WHERE email = lower(trim(p_email)) AND status = 'active' FOR UPDATE;
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  IF o.oidc_subject IS NULL THEN
    UPDATE operators SET oidc_issuer = p_issuer, oidc_subject = p_subject WHERE id = o.id;
    RETURN o.id;
  END IF;
  IF o.oidc_issuer = p_issuer AND o.oidc_subject = p_subject THEN
    RETURN o.id;
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_ops_operator_add(text, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_operator_disable(text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_operator_reset(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_operators() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_enrol_issue(text, bytea, interval, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_enrol_check(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_enrol_complete(bytea, uuid, bytea, bytea, int, bigint, bytea, boolean, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_credential_ids(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_challenge_issue(bytea, text, uuid, interval, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_challenge_take(bytea, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_credential_find(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_credential_used(bytea, bigint, bigint) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_session_start(bytea, uuid, text, interval, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_session(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_session_end(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_sso_begin(bytea, text, text, bytea, interval) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_sso_take(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_ops_sso_operator(text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_operator_add(text, text, text), taskiem_ops_operator_disable(text, text), taskiem_ops_operator_reset(text),
  taskiem_ops_operators(), taskiem_ops_enrol_issue(text, bytea, interval, text), taskiem_ops_enrol_check(bytea),
  taskiem_ops_enrol_complete(bytea, uuid, bytea, bytea, int, bigint, bytea, boolean, text), taskiem_ops_credential_ids(uuid),
  taskiem_ops_challenge_issue(bytea, text, uuid, interval, text), taskiem_ops_challenge_take(bytea, text, text), taskiem_ops_credential_find(bytea),
  taskiem_ops_credential_used(bytea, bigint, bigint), taskiem_ops_session_start(bytea, uuid, text, interval, text), taskiem_ops_session(bytea),
  taskiem_ops_session_end(bytea), taskiem_ops_sso_begin(bytea, text, text, bytea, interval), taskiem_ops_sso_take(bytea),
  taskiem_ops_sso_operator(text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_operator_add(text, text, text), taskiem_ops_operator_disable(text, text), taskiem_ops_operator_reset(text),
  taskiem_ops_operators(), taskiem_ops_enrol_issue(text, bytea, interval, text), taskiem_ops_enrol_check(bytea),
  taskiem_ops_enrol_complete(bytea, uuid, bytea, bytea, int, bigint, bytea, boolean, text), taskiem_ops_credential_ids(uuid),
  taskiem_ops_challenge_issue(bytea, text, uuid, interval, text), taskiem_ops_challenge_take(bytea, text, text), taskiem_ops_credential_find(bytea),
  taskiem_ops_credential_used(bytea, bigint, bigint), taskiem_ops_session_start(bytea, uuid, text, interval, text), taskiem_ops_session(bytea),
  taskiem_ops_session_end(bytea), taskiem_ops_sso_begin(bytea, text, text, bytea, interval), taskiem_ops_sso_take(bytea),
  taskiem_ops_sso_operator(text, text, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_ops_sso_operator(text, text, text);
DROP FUNCTION taskiem_ops_sso_take(bytea);
DROP FUNCTION taskiem_ops_sso_begin(bytea, text, text, bytea, interval);
DROP FUNCTION taskiem_ops_session_end(bytea);
DROP FUNCTION taskiem_ops_session(bytea);
DROP FUNCTION taskiem_ops_session_start(bytea, uuid, text, interval, text);
DROP FUNCTION taskiem_ops_credential_used(bytea, bigint, bigint);
DROP FUNCTION taskiem_ops_credential_find(bytea);
DROP FUNCTION taskiem_ops_challenge_take(bytea, text, text);
DROP FUNCTION taskiem_ops_challenge_issue(bytea, text, uuid, interval, text);
DROP FUNCTION taskiem_ops_credential_ids(uuid);
DROP FUNCTION taskiem_ops_enrol_complete(bytea, uuid, bytea, bytea, int, bigint, bytea, boolean, text);
DROP FUNCTION taskiem_ops_enrol_check(bytea);
DROP FUNCTION taskiem_ops_enrol_issue(text, bytea, interval, text);
DROP FUNCTION taskiem_ops_operators();
DROP FUNCTION taskiem_ops_operator_reset(text);
DROP FUNCTION taskiem_ops_operator_disable(text, text);
DROP FUNCTION taskiem_ops_operator_add(text, text, text);
DROP TABLE operator_sso_requests;
DROP TABLE operator_sessions;
DROP TABLE operator_challenges;
DROP TABLE operator_enrolments;
DROP TABLE operator_credentials;
DROP TABLE operators;
ALTER TABLE audit_anchors DISABLE TRIGGER audit_anchors_append_only;
DELETE FROM audit_anchors WHERE tenant_id = 'ffffffff-ffff-ffff-ffff-ffffffffffff';
ALTER TABLE audit_anchors ENABLE TRIGGER audit_anchors_append_only;
ALTER TABLE audit_anchors ADD CONSTRAINT audit_anchors_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES tenants(id);
ALTER TABLE tenants DROP CONSTRAINT tenants_not_platform_chain;
