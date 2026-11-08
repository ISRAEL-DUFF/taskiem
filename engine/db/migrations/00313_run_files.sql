-- Files a workflow run fetched or made: a customer's voice note, a reply
-- spoken by a speech step, a document a step downloaded (decision 0062,
-- docs/files.md). The content is in the organisation's own Xenibase
-- storage (the private bucket); a run carries only a reference
-- {file, name, type, size, sha256}. Every file has an end: the sweep
-- deletes the object and the row once expires_at has passed.

-- +goose Up
CREATE TABLE run_files (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  workflow_id  uuid,
  run_id       uuid,
  name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
  type         text NOT NULL,
  size         bigint NOT NULL CHECK (size > 0),
  sha256       bytea NOT NULL CHECK (length(sha256) = 32),
  storage_key  text NOT NULL,
  -- Sent by a person (a customer's voice note): personal data, scanned
  -- before it is kept.
  personal     boolean NOT NULL DEFAULT false,
  expires_at   timestamptz NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX run_files_run ON run_files (tenant_id, run_id);
CREATE INDEX run_files_expiry ON run_files (expires_at);
ALTER TABLE run_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE run_files FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON run_files TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON run_files TO taskiem_app;

-- Retention across tenants: the dispatch role sees only expired files'
-- ids, tenants and storage keys (never names or content), for the sweep
-- to delete the stored objects in each tenant's storage, then the rows.
CREATE POLICY dispatch_read_expired ON run_files FOR SELECT TO taskiem_dispatch USING (expires_at <= now());
GRANT SELECT (id, tenant_id, storage_key, expires_at) ON run_files TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_run_files_expired(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, storage_key text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT f.id, f.tenant_id, f.storage_key FROM run_files f
   WHERE f.expires_at <= now()
   ORDER BY f.expires_at
   LIMIT LEAST(GREATEST(COALESCE(p_limit, 100), 1), 1000)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_run_files_expired(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_run_files_expired(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_run_files_expired(int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_run_files_expired(int);
DROP TABLE run_files;
