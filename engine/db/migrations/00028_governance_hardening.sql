-- Governance hardening (spec 9.1, 10.3, 13.3):
--  * an API key belongs to the person who made it: keys act within their
--    owner's current permissions, and four-eyes and maker-checker see
--    through a key to its owner;
--  * a Git-led connection to a governed environment waits for a second
--    person, like a publish;
--  * push webhooks are received once;
--  * the platform's own Git secrets live in the reserved _git environment,
--    out of reach of workflows and of the secrets API.

-- +goose Up
ALTER TABLE api_keys ADD COLUMN owner_id uuid REFERENCES users(id);

-- A key made by a person is theirs; a key made with a key belongs to that
-- key's owner. Keys whose maker cannot be traced keep NULL and stop working.
UPDATE api_keys k SET owner_id = u.id FROM users u WHERE k.created_by = u.id::text;
-- +goose StatementBegin
DO $$
BEGIN
  FOR i IN 1..16 LOOP
    UPDATE api_keys k SET owner_id = p.owner_id FROM api_keys p
     WHERE k.owner_id IS NULL AND k.created_by = 'key:' || p.id::text AND p.owner_id IS NOT NULL;
    EXIT WHEN NOT FOUND;
  END LOOP;
END
$$;
-- +goose StatementEnd

GRANT SELECT (owner_id) ON api_keys TO taskiem_dispatch;

DROP FUNCTION taskiem_auth_api_key(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_api_key(p_hash bytea)
RETURNS TABLE (key_id uuid, tenant_id uuid, permissions text[], environment text, owner_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.id, k.tenant_id, k.permissions, k.environment, k.owner_id FROM api_keys k
    JOIN tenants t ON t.id = k.tenant_id AND t.status = 'active'
   WHERE k.key_hash = p_hash AND k.revoked_at IS NULL AND k.expires_at > now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_api_key(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_api_key(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_api_key(bytea) TO taskiem_app;

-- The person behind an actor: "key:<id>" (or a bare key id, as recorded
-- in published_by and deployed_by) is the key's owner; anything else is
-- itself. Runs with the caller's rights, so it sees only the tenant's keys.
-- +goose StatementBegin
CREATE FUNCTION taskiem_actor_human(p_actor text)
RETURNS text LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT k.owner_id::text FROM api_keys k
                    WHERE k.id::text = CASE WHEN p_actor LIKE 'key:%' THEN substr(p_actor, 5) ELSE p_actor END), p_actor)
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION taskiem_actor_human(text) TO taskiem_app;

-- A change to a Git-led connection waiting for a second person. New
-- credentials wait in the vault (_git/pending_credentials_<env>).
CREATE TABLE git_connection_requests (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  environment   text NOT NULL,
  provider      text NOT NULL,
  api_url       text NOT NULL DEFAULT '',
  repo          text NOT NULL,
  branch        text NOT NULL,
  path          text NOT NULL,
  tests_path    text NOT NULL,
  mode          text NOT NULL,
  credentials   boolean NOT NULL DEFAULT false, -- new credentials wait in the vault
  requested_by  uuid NOT NULL REFERENCES users(id),
  requested_at  timestamptz NOT NULL DEFAULT now(),
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'withdrawn')),
  decided_by    uuid REFERENCES users(id),
  decided_at    timestamptz,
  comment       text,
  PRIMARY KEY (tenant_id, environment)
);

-- Push webhook deliveries already received, by the host's delivery id.
CREATE TABLE git_push_receipts (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  delivery_id  text NOT NULL,
  commit       text NOT NULL,
  received_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment, delivery_id)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['git_connection_requests', 'git_push_receipts'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- Git credentials and webhook secrets move to the reserved _git
-- environment. A secret's ciphertext is bound to its id, not its name.
UPDATE secrets SET name = 'credentials_' || environment, environment = '_git'
 WHERE name = 'git_credentials' AND environment NOT LIKE '\_%';
UPDATE secrets SET name = 'webhook_secret_' || environment, environment = '_git'
 WHERE name = 'git_webhook_secret' AND environment NOT LIKE '\_%';

-- +goose Down
DELETE FROM secrets WHERE environment = '_git' AND name LIKE 'pending\_credentials\_%';
UPDATE secrets SET environment = substr(name, length('credentials_') + 1), name = 'git_credentials'
 WHERE environment = '_git' AND name LIKE 'credentials\_%';
UPDATE secrets SET environment = substr(name, length('webhook_secret_') + 1), name = 'git_webhook_secret'
 WHERE environment = '_git' AND name LIKE 'webhook\_secret\_%';
DROP TABLE git_push_receipts;
DROP TABLE git_connection_requests;
DROP FUNCTION taskiem_actor_human(text);
DROP FUNCTION taskiem_auth_api_key(bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_api_key(p_hash bytea)
RETURNS TABLE (key_id uuid, tenant_id uuid, permissions text[], environment text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT k.id, k.tenant_id, k.permissions, k.environment FROM api_keys k
    JOIN tenants t ON t.id = k.tenant_id AND t.status = 'active'
   WHERE k.key_hash = p_hash AND k.revoked_at IS NULL AND k.expires_at > now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_api_key(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_api_key(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_api_key(bytea) TO taskiem_app;
REVOKE SELECT (owner_id) ON api_keys FROM taskiem_dispatch;
ALTER TABLE api_keys DROP COLUMN owner_id;
