-- Bring your own key (spec 14.1; decision 0019; docs/byok.md), background
-- re-wrapping after a rotation (self-review S23) and secrets bound to their
-- environment and name (S33).
--
-- tenant_byok_keys: a key the tenant controls in its own KMS (OpenBao or
-- Vault transit, AWS KMS, Google Cloud KMS, Azure Key Vault). While one is
-- in use, each new tenant key version is wrapped twice: by the platform's
-- KMS key, then by the customer's key (tenant_keys.byok_key_id), so neither
-- side alone can unwrap it. config locates the key and holds no secret;
-- credentials are sealed by the platform's KMS key, bound to the tenant and
-- the row, and are never readable through the API or by workflows.
-- check_ciphertext is a canary wrapped at onboarding: the health check
-- unwraps it and compares its digest.
--
-- tenant_pseudonym_keys: the HMAC key behind subject ids (spec 5.2). It was
-- tenant key version 1 itself; it is now its own key, sealed under the
-- current tenant key and re-wrapped with every rotation, so version 1 can
-- be retired. Its material starts as version 1's, so existing subject ids
-- do not change.
--
-- key_rewrap_due: tenants with data keys, subject keys or the pseudonym key
-- still under an older tenant key version (or secrets still on the first
-- encryption context). The scheduler re-wraps them in batches, retires the
-- old versions, and after a grace period destroys versions nothing uses.

-- +goose Up
CREATE TABLE tenant_byok_keys (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  provider          text NOT NULL CHECK (provider IN ('vault_transit', 'aws_kms', 'gcp_kms', 'azure_key_vault')),
  config            jsonb NOT NULL,                  -- address, mount, key, region...; never credentials
  description       text NOT NULL,                   -- e.g. "AWS KMS key alias/taskiem in af-south-1"
  credentials       text NOT NULL,                   -- platform-KMS ciphertext; '' once forgotten
  credentials_digest text NOT NULL,                  -- short fingerprint, to show which set is stored
  kms_key           text NOT NULL,                   -- platform root key name sealing the credentials
  check_ciphertext  text NOT NULL,                   -- canary wrapped by the customer's key
  check_digest      bytea NOT NULL CHECK (length(check_digest) = 32),
  status            text NOT NULL CHECK (status IN ('active', 'unavailable', 'retired')),
  checked_at        timestamptz,
  last_ok_at        timestamptz,
  failing_since     timestamptz,                    -- first failed check of the current streak
  check_failures    int NOT NULL DEFAULT 0,          -- consecutive failed checks
  last_error        text,
  recovered_at      timestamptz,
  created_by        text NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  retired_at        timestamptz,
  UNIQUE (tenant_id, id),
  CHECK ((status = 'retired') = (retired_at IS NOT NULL))
);
-- At most one customer key wraps new tenant key versions.
CREATE UNIQUE INDEX tenant_byok_keys_in_use ON tenant_byok_keys (tenant_id) WHERE status <> 'retired';

ALTER TABLE tenant_keys ADD COLUMN byok_key_id uuid,
  ADD COLUMN destroyed_at timestamptz,
  ADD FOREIGN KEY (tenant_id, byok_key_id) REFERENCES tenant_byok_keys (tenant_id, id);

CREATE TABLE tenant_pseudonym_keys (
  tenant_id    uuid PRIMARY KEY REFERENCES tenants(id),
  wrapped_key  bytea NOT NULL,                       -- nonce || sealed key, under kek_version
  kek_version  int NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (tenant_id, kek_version) REFERENCES tenant_keys (tenant_id, version)
);

-- 1: associated data is the secret's id. 2: tenant, environment, name (or
-- the owning connection) and id.
ALTER TABLE secrets ADD COLUMN aad_version smallint NOT NULL DEFAULT 1 CHECK (aad_version IN (1, 2));
CREATE INDEX secrets_rewrap ON secrets (tenant_id, kek_version);
CREATE INDEX subject_keys_rewrap ON subject_keys (tenant_id, kek_version) WHERE wrapped_key IS NOT NULL;

CREATE TABLE key_rewrap_due (
  tenant_id   uuid PRIMARY KEY REFERENCES tenants(id),
  reason      text NOT NULL,                          -- rotation, byok, migration
  since       timestamptz NOT NULL DEFAULT now(),
  not_before  timestamptz NOT NULL DEFAULT now(),
  last_error  text
);
-- Existing tenants: secrets move to the second encryption context and the
-- pseudonym key gets its own row.
INSERT INTO key_rewrap_due (tenant_id, reason)
  SELECT DISTINCT tenant_id, 'migration' FROM tenant_keys;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['tenant_byok_keys', 'tenant_pseudonym_keys', 'key_rewrap_due'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
  END LOOP;
END
$$;
-- +goose StatementEnd
GRANT SELECT, INSERT, UPDATE ON tenant_byok_keys, tenant_pseudonym_keys TO taskiem_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON key_rewrap_due TO taskiem_app;

-- Routing for the key job (ids and states only; the work runs in each
-- tenant's scope).
CREATE POLICY dispatch ON tenant_byok_keys FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch ON key_rewrap_due FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, status) ON tenant_byok_keys TO taskiem_dispatch;
GRANT SELECT (tenant_id, not_before) ON key_rewrap_due TO taskiem_dispatch;

-- +goose Down
-- Rolling back is for development: tenant keys wrapped by a customer key
-- and secrets sealed under the second context cannot be read by code from
-- before this migration.
REVOKE SELECT (tenant_id, not_before) ON key_rewrap_due FROM taskiem_dispatch;
REVOKE SELECT (tenant_id, status) ON tenant_byok_keys FROM taskiem_dispatch;
DROP TABLE key_rewrap_due;
DROP INDEX subject_keys_rewrap;
DROP INDEX secrets_rewrap;
ALTER TABLE secrets DROP COLUMN aad_version;
DROP TABLE tenant_pseudonym_keys;
ALTER TABLE tenant_keys DROP COLUMN byok_key_id, DROP COLUMN destroyed_at;
DROP TABLE tenant_byok_keys;
