-- Secrets, connections, variables, egress allow-lists. Spec 5.2, 13.1, 14.1, 14.2.

-- +goose Up
-- Each tenant has key-encryption keys (KEKs), stored only wrapped by the
-- root key in the KMS (OpenBao transit when self-hosted). Rotation adds a
-- version; data keys are re-wrapped, payloads are not re-encrypted.
CREATE TABLE tenant_keys (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  version      int  NOT NULL CHECK (version >= 1),
  wrapped_kek  text NOT NULL,                     -- KMS ciphertext
  kms_key      text NOT NULL,                     -- root key name in the KMS
  created_at   timestamptz NOT NULL DEFAULT now(),
  retired_at   timestamptz,
  PRIMARY KEY (tenant_id, version)
);

-- A secret: AES-256-GCM ciphertext under its own data key, which is wrapped
-- by the tenant KEK version kek_version.
CREATE TABLE secrets (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  name         text,                              -- NULL for connection-owned secrets
  ciphertext   bytea NOT NULL,                    -- nonce || sealed value
  wrapped_key  bytea NOT NULL,                    -- nonce || sealed data key
  kek_version  int   NOT NULL,
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id, kek_version) REFERENCES tenant_keys (tenant_id, version)
);
CREATE UNIQUE INDEX secrets_name ON secrets (tenant_id, environment, name) WHERE name IS NOT NULL;

CREATE TABLE connections (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  connector    text NOT NULL,                     -- connector id, e.g. paystack
  name         text NOT NULL,
  auth_type    text NOT NULL CHECK (auth_type IN ('api_key', 'oauth2', 'basic', 'custom', 'none')),
  secret_ref   uuid REFERENCES secrets(id),       -- JSON object of credential fields
  status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'expired')),
  expires_at   timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, environment, connector, name)
);

-- Tenant variables: the `env` root of expressions, per environment.
CREATE TABLE variables (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  name         text NOT NULL CHECK (name ~ '^[a-z][a-z0-9_]{0,62}$'),
  value        jsonb NOT NULL,
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment, name)
);

-- Hosts http steps and sandbox fetch may reach (connectors use their
-- manifest's hosts). Exact names or "*.example.com".
CREATE TABLE egress_rules (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  host         text NOT NULL CHECK (host ~ '^(\*\.)?[a-z0-9.-]+$'),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment, host)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['tenant_keys', 'secrets', 'connections', 'variables', 'egress_rules'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON tenant_keys TO taskiem_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON secrets, connections, variables, egress_rules TO taskiem_app;
