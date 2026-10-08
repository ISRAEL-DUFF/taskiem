-- +goose Up
-- Self-review K6. Secrets sealed before migration 00100 (aad_version 1)
-- are bound to their id only, so until the key job upgrades them to the
-- second encryption context, someone with database write access could
-- rename one or move it to another environment (or point another
-- connection at it) and it would still decrypt there.
--
-- This records, once, where each such secret was: its environment and
-- what it is bound to (its name, or "connection:<id>"). The application
-- role can read and delete these rows, never write them, so an
-- application credential cannot rewrite the record. A legacy secret is
-- read, and upgraded to the second context, only where it was recorded;
-- anywhere else, or with no record, it is refused.
CREATE TABLE secret_legacy_bindings (
  secret_id    uuid PRIMARY KEY REFERENCES secrets(id) ON DELETE CASCADE,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  binding      text NOT NULL,
  recorded_at  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO secret_legacy_bindings (secret_id, tenant_id, environment, binding)
  SELECT s.id, s.tenant_id, s.environment,
         COALESCE(s.name, 'connection:' || (SELECT c.id FROM connections c WHERE c.secret_ref = s.id LIMIT 1)::text, '')
    FROM secrets s
   WHERE s.aad_version < 2;

ALTER TABLE secret_legacy_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE secret_legacy_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON secret_legacy_bindings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, DELETE ON secret_legacy_bindings TO taskiem_app;

-- +goose Down
DROP TABLE secret_legacy_bindings;
