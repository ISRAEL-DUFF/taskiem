-- Plan caps and soft ingest limits (spec 8.3, 16). Each tenant's limits
-- are the platform defaults (TASKIEM_DEFAULT_*) overridden per column by
-- tenant_limits, which only operators write (taskiem tenants limits).
-- Runs a tenant's limits hold back wait as queued with queue_reason
-- 'tenant' and are admitted by the scheduler at the tenant's rate;
-- workers claim tasks round-robin across tenants under a per-tenant cap.

-- +goose Up
CREATE TABLE tenant_limits (
  tenant_id            uuid PRIMARY KEY REFERENCES tenants(id),
  ingest_rate          double precision CHECK (ingest_rate >= 0),     -- deliveries/s that start runs at once (soft)
  ingest_burst         int CHECK (ingest_burst >= 0),
  ingest_ceiling       double precision CHECK (ingest_ceiling >= 0),  -- deliveries/s above which 429 (hard)
  ingest_ceiling_burst int CHECK (ingest_ceiling_burst >= 0),
  max_running_runs     int CHECK (max_running_runs >= 0),
  max_queued_runs      int CHECK (max_queued_runs >= 0),
  runs_per_day         bigint CHECK (runs_per_day >= 0),
  runs_per_month       bigint CHECK (runs_per_month >= 0),
  max_workflows        int CHECK (max_workflows >= 0),
  max_steps_per_run    int CHECK (max_steps_per_run >= 0),
  worker_concurrency   int CHECK (worker_concurrency >= 0),
  max_payload_bytes    int CHECK (max_payload_bytes >= 0),
  max_secrets          int CHECK (max_secrets >= 0),
  max_connections      int CHECK (max_connections >= 0),
  updated_by           text NOT NULL,
  updated_at           timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE tenant_limits ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_limits FORCE ROW LEVEL SECURITY;
-- Tenants read their limits; nothing in the application writes them.
CREATE POLICY tenant_isolation ON tenant_limits FOR SELECT TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON tenant_limits TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT ON tenant_limits TO taskiem_app;
GRANT SELECT, INSERT, UPDATE ON tenant_limits TO taskiem_dispatch;

-- Runs started per UTC day: the plan quotas count these.
CREATE TABLE tenant_usage (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  day          date NOT NULL,
  runs_started bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, day)
);
ALTER TABLE tenant_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_usage TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON tenant_usage TO taskiem_app;

-- The admission token bucket of each tenant with queued runs.
CREATE TABLE tenant_admission (
  tenant_id   uuid PRIMARY KEY REFERENCES tenants(id),
  tokens      double precision NOT NULL DEFAULT 0,
  refilled_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE tenant_admission ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_admission FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_admission TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON tenant_admission TO taskiem_app;

-- Limits a tenant reached, per limit and UTC day: shown with the limits and
-- watched by alert rules of kind 'limit'.
CREATE TABLE tenant_limit_hits (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  limit_name text NOT NULL,
  day        date NOT NULL,
  hits       bigint NOT NULL DEFAULT 1,
  first_at   timestamptz NOT NULL DEFAULT now(),
  last_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, limit_name, day)
);
ALTER TABLE tenant_limit_hits ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_limit_hits FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_limit_hits TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON tenant_limit_hits TO taskiem_app;
CREATE INDEX tenant_limit_hits_recent ON tenant_limit_hits (tenant_id, last_at);

-- Why a queued run waits: its workflow's concurrency (spec 4.8), or its
-- tenant's limits (spec 8.3, 16).
ALTER TABLE runs ADD COLUMN queue_reason text CHECK (queue_reason IN ('workflow', 'tenant'));
ALTER TABLE runs ADD COLUMN steps_scheduled int NOT NULL DEFAULT 0;
UPDATE runs SET queue_reason = 'workflow' WHERE status = 'queued';
CREATE INDEX runs_tenant_queue ON runs (tenant_id, started_at) WHERE status = 'queued' AND queue_reason = 'tenant';
CREATE INDEX runs_tenant_queued ON runs (tenant_id) WHERE status = 'queued';
CREATE INDEX runs_tenant_running ON runs (tenant_id) WHERE status = 'running';
GRANT SELECT (status, queue_reason) ON runs TO taskiem_dispatch;

CREATE INDEX tasks_claimable_tenant ON tasks (queue, tenant_id, priority, available_at) WHERE lease_owner IS NULL;
CREATE INDEX tasks_leased_tenant ON tasks (tenant_id, queue) WHERE lease_owner IS NOT NULL;

ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check CHECK (kind IN ('run_failed', 'slow_run', 'stuck_approval', 'needs_reconciliation',
  'connector_drift', 'credential_expiry', 'audit_anchor', 'limit'));

-- Operators set a tenant's limits (the CLI, inside the tenant's scope).
-- A key set to JSON null returns to the platform default.
-- +goose StatementBegin
CREATE FUNCTION taskiem_set_tenant_limits(p_tenant uuid, p_values jsonb, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  k text;
  known text[] := ARRAY['ingest_rate', 'ingest_burst', 'ingest_ceiling', 'ingest_ceiling_burst', 'max_running_runs', 'max_queued_runs',
    'runs_per_day', 'runs_per_month', 'max_workflows', 'max_steps_per_run', 'worker_concurrency', 'max_payload_bytes', 'max_secrets', 'max_connections'];
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  FOR k IN SELECT jsonb_object_keys(p_values) LOOP
    IF NOT (k = ANY (known)) THEN
      RAISE EXCEPTION 'unknown limit %', k USING ERRCODE = '22023';
    END IF;
  END LOOP;
  INSERT INTO tenant_limits (tenant_id, updated_by) VALUES (p_tenant, p_by)
    ON CONFLICT (tenant_id) DO UPDATE SET updated_by = EXCLUDED.updated_by, updated_at = now();
  UPDATE tenant_limits l SET
    ingest_rate          = CASE WHEN p_values ? 'ingest_rate'          THEN (p_values->>'ingest_rate')::double precision ELSE l.ingest_rate END,
    ingest_burst         = CASE WHEN p_values ? 'ingest_burst'         THEN (p_values->>'ingest_burst')::int ELSE l.ingest_burst END,
    ingest_ceiling       = CASE WHEN p_values ? 'ingest_ceiling'       THEN (p_values->>'ingest_ceiling')::double precision ELSE l.ingest_ceiling END,
    ingest_ceiling_burst = CASE WHEN p_values ? 'ingest_ceiling_burst' THEN (p_values->>'ingest_ceiling_burst')::int ELSE l.ingest_ceiling_burst END,
    max_running_runs     = CASE WHEN p_values ? 'max_running_runs'     THEN (p_values->>'max_running_runs')::int ELSE l.max_running_runs END,
    max_queued_runs      = CASE WHEN p_values ? 'max_queued_runs'      THEN (p_values->>'max_queued_runs')::int ELSE l.max_queued_runs END,
    runs_per_day         = CASE WHEN p_values ? 'runs_per_day'         THEN (p_values->>'runs_per_day')::bigint ELSE l.runs_per_day END,
    runs_per_month       = CASE WHEN p_values ? 'runs_per_month'       THEN (p_values->>'runs_per_month')::bigint ELSE l.runs_per_month END,
    max_workflows        = CASE WHEN p_values ? 'max_workflows'        THEN (p_values->>'max_workflows')::int ELSE l.max_workflows END,
    max_steps_per_run    = CASE WHEN p_values ? 'max_steps_per_run'    THEN (p_values->>'max_steps_per_run')::int ELSE l.max_steps_per_run END,
    worker_concurrency   = CASE WHEN p_values ? 'worker_concurrency'   THEN (p_values->>'worker_concurrency')::int ELSE l.worker_concurrency END,
    max_payload_bytes    = CASE WHEN p_values ? 'max_payload_bytes'    THEN (p_values->>'max_payload_bytes')::int ELSE l.max_payload_bytes END,
    max_secrets          = CASE WHEN p_values ? 'max_secrets'          THEN (p_values->>'max_secrets')::int ELSE l.max_secrets END,
    max_connections      = CASE WHEN p_values ? 'max_connections'      THEN (p_values->>'max_connections')::int ELSE l.max_connections END
  WHERE l.tenant_id = p_tenant;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_set_tenant_limits(uuid, jsonb, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_set_tenant_limits(uuid, jsonb, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_set_tenant_limits(uuid, jsonb, text) TO taskiem_app;

-- Workers: claim up to p_limit tasks from a queue, round-robin across
-- tenants (each tenant's first task, then each one's second, ...), and no
-- tenant beyond its in-flight cap on this queue: its worker_concurrency, or
-- p_tenant_cap (0: no cap). A tenant's own tasks go highest priority first.
DROP FUNCTION taskiem_claim_tasks(text, text, int, interval);
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval DEFAULT '60 seconds', p_tenant_cap int DEFAULT 0)
RETURNS TABLE (task_id uuid, tenant_id uuid, run_id uuid, step_id text, attempt int, lease_epoch bigint)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH RECURSIVE ready AS (
    (SELECT t.tenant_id FROM tasks t WHERE t.queue = p_queue AND t.lease_owner IS NULL ORDER BY t.tenant_id LIMIT 1)
    UNION ALL
    SELECT (SELECT t.tenant_id FROM tasks t WHERE t.queue = p_queue AND t.lease_owner IS NULL AND t.tenant_id > r.tenant_id ORDER BY t.tenant_id LIMIT 1)
      FROM ready r WHERE r.tenant_id IS NOT NULL),
  room AS (
    SELECT r.tenant_id, CASE WHEN COALESCE(l.worker_concurrency, p_tenant_cap) <= 0 THEN p_limit
      ELSE LEAST(p_limit, COALESCE(l.worker_concurrency, p_tenant_cap) - (SELECT count(*) FROM tasks x
        WHERE x.tenant_id = r.tenant_id AND x.queue = p_queue AND x.lease_owner IS NOT NULL)::int) END AS n
      FROM ready r LEFT JOIN tenant_limits l ON l.tenant_id = r.tenant_id
     WHERE r.tenant_id IS NOT NULL),
  cand AS (
    SELECT c.id, c.priority, c.available_at, row_number() OVER (PARTITION BY room.tenant_id ORDER BY c.priority, c.available_at) AS rn
      FROM room, LATERAL (
        SELECT t.id, t.priority, t.available_at FROM tasks t
         WHERE t.queue = p_queue AND t.tenant_id = room.tenant_id AND t.lease_owner IS NULL AND t.available_at <= now()
         ORDER BY t.priority, t.available_at
         LIMIT GREATEST(room.n, 0)
         FOR UPDATE SKIP LOCKED) c),
  pick AS (SELECT cand.id FROM cand ORDER BY cand.rn, cand.priority, cand.available_at LIMIT p_limit)
  UPDATE tasks t
     SET lease_owner = p_worker, lease_until = now() + p_lease, lease_epoch = t.lease_epoch + 1
    FROM pick WHERE t.id = pick.id
  RETURNING t.id, t.tenant_id, t.run_id, t.step_id, t.attempt, t.lease_epoch
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_tasks(text, text, int, interval, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int) TO taskiem_app;

-- Scheduler: tenants with runs waiting for tenant admission, the longest
-- waiting first. Routing columns only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_backlog_tenants(p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH RECURSIVE t AS (
    (SELECT r.tenant_id FROM runs r WHERE r.status = 'queued' AND r.queue_reason = 'tenant' ORDER BY r.tenant_id LIMIT 1)
    UNION ALL
    SELECT (SELECT r.tenant_id FROM runs r WHERE r.status = 'queued' AND r.queue_reason = 'tenant' AND r.tenant_id > t.tenant_id ORDER BY r.tenant_id LIMIT 1)
      FROM t WHERE t.tenant_id IS NOT NULL)
  SELECT t.tenant_id FROM t WHERE t.tenant_id IS NOT NULL
   ORDER BY (SELECT min(r.started_at) FROM runs r WHERE r.tenant_id = t.tenant_id AND r.status = 'queued' AND r.queue_reason = 'tenant')
   LIMIT p_limit
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_backlog_tenants(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_backlog_tenants(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_backlog_tenants(int) TO taskiem_app;

-- Metrics: queued runs by reason across tenants, counts only (no tenant ids,
-- so the series stay few however many tenants there are).
-- +goose StatementBegin
CREATE FUNCTION taskiem_backlog_stats()
RETURNS TABLE (reason text, runs bigint, tenants bigint, max_per_tenant bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT q.reason, sum(q.n)::bigint, count(*)::bigint, max(q.n)::bigint FROM (
    SELECT COALESCE(r.queue_reason, 'workflow') AS reason, r.tenant_id, count(*) AS n
      FROM runs r WHERE r.status = 'queued' GROUP BY 1, 2) q
   GROUP BY q.reason
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_backlog_stats() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_backlog_stats() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_backlog_stats() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_backlog_stats();
DROP FUNCTION taskiem_backlog_tenants(int);
DROP FUNCTION taskiem_claim_tasks(text, text, int, interval, int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval DEFAULT '60 seconds')
RETURNS TABLE (task_id uuid, tenant_id uuid, run_id uuid, step_id text, attempt int, lease_epoch bigint)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH c AS (
    SELECT t.id FROM tasks t
     WHERE t.queue = p_queue AND t.lease_owner IS NULL AND t.available_at <= now()
     ORDER BY t.priority, t.available_at
     LIMIT p_limit
     FOR UPDATE SKIP LOCKED)
  UPDATE tasks t
     SET lease_owner = p_worker, lease_until = now() + p_lease, lease_epoch = t.lease_epoch + 1
    FROM c WHERE t.id = c.id
  RETURNING t.id, t.tenant_id, t.run_id, t.step_id, t.attempt, t.lease_epoch
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_tasks(text, text, int, interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval) TO taskiem_app;
DROP FUNCTION taskiem_set_tenant_limits(uuid, jsonb, text);
DELETE FROM alert_rules WHERE kind = 'limit';
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_kind_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_kind_check CHECK (kind IN ('run_failed', 'slow_run', 'stuck_approval', 'needs_reconciliation',
  'connector_drift', 'credential_expiry', 'audit_anchor'));
DROP INDEX tasks_leased_tenant;
DROP INDEX tasks_claimable_tenant;
REVOKE SELECT (status, queue_reason) ON runs FROM taskiem_dispatch;
DROP INDEX runs_tenant_running;
DROP INDEX runs_tenant_queued;
DROP INDEX runs_tenant_queue;
ALTER TABLE runs DROP COLUMN steps_scheduled;
ALTER TABLE runs DROP COLUMN queue_reason;
DROP TABLE tenant_limit_hits;
DROP TABLE tenant_admission;
DROP TABLE tenant_usage;
DROP TABLE tenant_limits;
