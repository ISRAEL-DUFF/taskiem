-- API identity, tenancy structure, approvals. Spec 9.1, 13.1–13.3.

-- +goose Up
CREATE TABLE workspaces (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  name        text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
ALTER TABLE workflows ADD COLUMN workspace_id uuid REFERENCES workspaces(id);

CREATE TABLE environments (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  name        text NOT NULL CHECK (name ~ '^[a-z][a-z0-9_-]{0,31}$'),
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, name)
);

ALTER TABLE workflow_versions ADD COLUMN created_by text;
ALTER TABLE workflow_versions ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE runs ADD COLUMN started_by text;

-- Sessions and API keys store only SHA-256 hashes of their tokens.
CREATE TABLE sessions (
  token_hash    bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  user_id       uuid NOT NULL REFERENCES users(id),
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  revoked_at    timestamptz,
  ip            text
);

CREATE TABLE api_keys (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  name         text NOT NULL,
  key_hash     bytea NOT NULL UNIQUE CHECK (length(key_hash) = 32),
  prefix       text NOT NULL,                    -- shown in lists; the key itself is shown once
  permissions  text[] NOT NULL,
  environment  text,                             -- NULL: every environment
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  last_used_at timestamptz,
  revoked_at   timestamptz
);

-- Open approval requests (the inbox) and individual decisions.
CREATE TABLE approvals (
  tenant_id     uuid NOT NULL,
  run_id        uuid NOT NULL REFERENCES runs(id),
  step_id       text NOT NULL,
  role          text,
  policy        text,
  required      int  NOT NULL DEFAULT 1,
  subject       jsonb,                           -- as recorded in the run: personal data stays sealed
  requested_at  timestamptz NOT NULL DEFAULT now(),
  timeout_at    timestamptz,
  status        text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'approved', 'rejected', 'expired', 'cancelled')),
  closed_at     timestamptz,
  PRIMARY KEY (run_id, step_id)
);
CREATE INDEX approvals_open ON approvals (tenant_id, role) WHERE status = 'open';

CREATE TABLE approval_decisions (
  tenant_id   uuid NOT NULL,
  run_id      uuid NOT NULL,
  step_id     text NOT NULL,
  user_id     uuid NOT NULL REFERENCES users(id),
  decision    text NOT NULL CHECK (decision IN ('approved', 'rejected')),
  channel     text NOT NULL,
  ip          text,
  shown       jsonb,                             -- exactly what the approver saw
  decided_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (run_id, step_id, user_id),        -- distinct approvers
  FOREIGN KEY (run_id, step_id) REFERENCES approvals (run_id, step_id)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['workspaces', 'environments', 'sessions', 'api_keys', 'approvals', 'approval_decisions'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE, DELETE ON workspaces, environments TO taskiem_app;
GRANT SELECT, INSERT, UPDATE ON sessions, api_keys, approvals TO taskiem_app;
GRANT SELECT, INSERT ON approval_decisions TO taskiem_app;

-- Authentication runs before any tenant scope exists: these two functions
-- are the only way to resolve a token, and return routing columns only.
CREATE POLICY dispatch ON sessions FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch ON api_keys FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (token_hash, user_id, tenant_id, expires_at, revoked_at) ON sessions TO taskiem_dispatch;
GRANT SELECT (id, tenant_id, key_hash, permissions, environment, expires_at, revoked_at) ON api_keys TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_session(p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.user_id, s.tenant_id FROM sessions s
   WHERE s.token_hash = p_hash AND s.revoked_at IS NULL AND s.expires_at > now()
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_api_key(p_hash bytea)
RETURNS TABLE (key_id uuid, tenant_id uuid, permissions text[], environment text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.id, k.tenant_id, k.permissions, k.environment FROM api_keys k
   WHERE k.key_hash = p_hash AND k.revoked_at IS NULL AND k.expires_at > now()
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_session(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_api_key(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_session(bytea), taskiem_auth_api_key(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_session(bytea), taskiem_auth_api_key(bytea) TO taskiem_app;
