-- Per-use secret.read auditing (spec 14.1, 9.2). Every decryption of a
-- tenant secret or connection credential for use is one row here: which
-- secret, for what purpose, by which run, step and attempt, when. Never the
-- value, nor a hash of it.
--
-- A busy tenant decrypts thousands of times a minute, so reads are kept out
-- of the hash-chained audit_log. Instead, once an hour has closed, the
-- scheduler appends one "secret.read.digest" entry per tenant-hour to the
-- chain: the count per secret and a SHA-256 over that hour's rows. A row
-- removed, changed or added afterwards no longer matches the digest the
-- chain (and its anchors) hold.

-- +goose Up
CREATE TABLE secret_reads (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  at            timestamptz NOT NULL DEFAULT date_trunc('microseconds', clock_timestamp()),
  kind          text NOT NULL CHECK (kind IN ('secret', 'connection', 'webhook', 'git', 'alert_channel', 'identity')),
  environment   text NOT NULL,
  name          text NOT NULL,       -- the secret's name, or the connection's
  connection_id uuid,                -- kind = 'connection'
  connector     text,                -- kind = 'connection'
  purpose       text NOT NULL,       -- step.connector, step.http, step.code, ingest.verify, git.sync, alert.deliver, ...
  run_id        uuid,                -- not a foreign key: reads outlive purged runs
  step_id       text,
  attempt       int,
  actor         text NOT NULL
);
CREATE INDEX secret_reads_tenant_at ON secret_reads (tenant_id, at);
CREATE INDEX secret_reads_name ON secret_reads (tenant_id, environment, name, at) WHERE connection_id IS NULL;
CREATE INDEX secret_reads_connection ON secret_reads (tenant_id, connection_id, at) WHERE connection_id IS NOT NULL;
CREATE INDEX secret_reads_run ON secret_reads (run_id) WHERE run_id IS NOT NULL;
-- The digest job's cross-tenant scan of recent hours.
CREATE INDEX secret_reads_at ON secret_reads USING brin (at);

ALTER TABLE secret_reads ENABLE ROW LEVEL SECURITY;
ALTER TABLE secret_reads FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON secret_reads FOR SELECT TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY tenant_record ON secret_reads FOR INSERT TO taskiem_app WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
-- Insert-only: the application can record a read but never change or remove one.
GRANT SELECT, INSERT ON secret_reads TO taskiem_app;

-- One row per tenant-hour digested into the audit chain.
CREATE TABLE secret_read_digests (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  hour       timestamptz NOT NULL,
  reads      bigint NOT NULL,
  sha256     bytea NOT NULL CHECK (length(sha256) = 32),
  chain_seq  bigint NOT NULL,
  made_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, hour)
);
ALTER TABLE secret_read_digests ENABLE ROW LEVEL SECURITY;
ALTER TABLE secret_read_digests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON secret_read_digests TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON secret_read_digests TO taskiem_app;

-- Neither table's rows are ever changed; digests are never removed.
-- +goose StatementBegin
CREATE FUNCTION taskiem_secret_reads_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION '% rows are append-only', TG_TABLE_NAME USING ERRCODE = 'check_violation';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER secret_reads_immutable BEFORE UPDATE ON secret_reads
  FOR EACH ROW EXECUTE FUNCTION taskiem_secret_reads_immutable();
CREATE TRIGGER secret_read_digests_immutable BEFORE UPDATE OR DELETE ON secret_read_digests
  FOR EACH ROW EXECUTE FUNCTION taskiem_secret_reads_immutable();

-- Tenant-hours with reads in [p_since, p_until) and no digest yet: what
-- the digest job works on. Routing columns only. Hours are UTC.
-- +goose StatementBegin
CREATE FUNCTION taskiem_secret_read_hours_due(p_since timestamptz, p_until timestamptz)
RETURNS TABLE (tenant_id uuid, hour timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT r.tenant_id, date_trunc('hour', r.at, 'UTC') FROM secret_reads r
   WHERE r.at >= p_since AND r.at < p_until
     AND NOT EXISTS (SELECT 1 FROM secret_read_digests d
                      WHERE d.tenant_id = r.tenant_id AND d.hour = date_trunc('hour', r.at, 'UTC'))
   ORDER BY 2, 1
$$;
-- +goose StatementEnd

-- Retention: rows older than p_keep (never less than 400 days) whose hour
-- has been digested. The digest stays in the chain for good.
-- +goose StatementBegin
CREATE FUNCTION taskiem_secret_reads_purge(p_keep interval, p_limit int)
RETURNS int
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH gone AS (
    DELETE FROM secret_reads WHERE id IN (
      SELECT r.id FROM secret_reads r
       WHERE r.at < now() - greatest(p_keep, interval '400 days')
         AND EXISTS (SELECT 1 FROM secret_read_digests d
                      WHERE d.tenant_id = r.tenant_id AND d.hour = date_trunc('hour', r.at, 'UTC'))
       LIMIT p_limit)
    RETURNING 1)
  SELECT count(*)::int FROM gone
$$;
-- +goose StatementEnd

CREATE POLICY dispatch_scan ON secret_reads FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_purge ON secret_reads FOR DELETE TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_scan ON secret_read_digests FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, at), DELETE ON secret_reads TO taskiem_dispatch;
GRANT SELECT (tenant_id, hour) ON secret_read_digests TO taskiem_dispatch;
ALTER FUNCTION taskiem_secret_read_hours_due(timestamptz, timestamptz) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_secret_reads_purge(interval, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_secret_read_hours_due(timestamptz, timestamptz) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION taskiem_secret_reads_purge(interval, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_secret_read_hours_due(timestamptz, timestamptz) TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_secret_reads_purge(interval, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_secret_reads_purge(interval, int);
DROP FUNCTION taskiem_secret_read_hours_due(timestamptz, timestamptz);
DROP TABLE secret_read_digests;
DROP TABLE secret_reads;
DROP FUNCTION taskiem_secret_reads_immutable();
