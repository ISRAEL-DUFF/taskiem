-- Dedicated worker pools (Phase 4 P4-3, decision 0024). A worker process
-- belongs to one pool (TASKIEM_WORKER_POOL, default "shared") and claims
-- only the tasks of tenants routed to that pool, on the queues it serves
-- (TASKIEM_WORKER_QUEUES). A tenant's pool is, in order:
--
--   1. its own assignment (tenant_worker_pools);
--   2. for a sub-tenant, its partner's own assignment;
--   3. its plan's assignment (plan_worker_pools; the subscription of the
--      tenant, or of its partner, as for limits);
--   4. "shared".
--
-- Operators assign with `taskiem pools assign` through the definer
-- functions below; every change is kept in worker_pool_changes, and a
-- tenant's also goes into its audit chain. The routing is read at claim
-- time, so a change moves queued tasks at once; leased tasks finish where
-- they are. Fair scheduling between the tenants of a pool is unchanged:
-- claims take round-robin across tenants, each under its concurrency cap.
--
-- worker_pool_workers records which pools and queues have live workers, so
-- an assignment to a pool nobody serves can be refused before it strands
-- work.

-- +goose Up
CREATE TABLE tenant_worker_pools (
  tenant_id    uuid PRIMARY KEY REFERENCES tenants(id),
  pool         text NOT NULL CHECK (pool ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
  assigned_by  text NOT NULL,
  assigned_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE tenant_worker_pools ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_worker_pools FORCE ROW LEVEL SECURITY;
-- A tenant may read its own routing; only the definer functions write.
CREATE POLICY tenant_isolation ON tenant_worker_pools FOR SELECT TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON tenant_worker_pools TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT ON tenant_worker_pools TO taskiem_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_worker_pools TO taskiem_dispatch;

CREATE TABLE plan_worker_pools (
  plan_id      text PRIMARY KEY REFERENCES plans(id),
  pool         text NOT NULL CHECK (pool ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
  assigned_by  text NOT NULL,
  assigned_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE plan_worker_pools ENABLE ROW LEVEL SECURITY;
ALTER TABLE plan_worker_pools FORCE ROW LEVEL SECURITY;
CREATE POLICY catalogue ON plan_worker_pools FOR SELECT TO taskiem_app USING (true);
CREATE POLICY dispatch ON plan_worker_pools TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT ON plan_worker_pools TO taskiem_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON plan_worker_pools TO taskiem_dispatch;

-- Every assignment and removal, append-only (pool NULL: back to the
-- default).
CREATE TABLE worker_pool_changes (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id   uuid REFERENCES tenants(id),
  plan_id     text,
  pool        text,
  changed_by  text NOT NULL,
  changed_at  timestamptz NOT NULL DEFAULT now(),
  CHECK ((tenant_id IS NULL) <> (plan_id IS NULL))
);
ALTER TABLE worker_pool_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE worker_pool_changes FORCE ROW LEVEL SECURITY;
CREATE POLICY operator ON worker_pool_changes FOR SELECT TO taskiem_app USING (true);
CREATE POLICY dispatch ON worker_pool_changes TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT ON worker_pool_changes TO taskiem_app;
GRANT SELECT, INSERT ON worker_pool_changes TO taskiem_dispatch;

-- Live workers by pool and queue (no tenant data): each worker process
-- reports every 30 seconds.
CREATE TABLE worker_pool_workers (
  worker_id  text NOT NULL,
  queue      text NOT NULL,
  pool       text NOT NULL,
  seen_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (worker_id, queue)
);
ALTER TABLE worker_pool_workers ENABLE ROW LEVEL SECURITY;
ALTER TABLE worker_pool_workers FORCE ROW LEVEL SECURITY;
CREATE POLICY operator ON worker_pool_workers FOR SELECT TO taskiem_app USING (true);
CREATE POLICY dispatch ON worker_pool_workers TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT ON worker_pool_workers TO taskiem_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON worker_pool_workers TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_tenant_worker_pool(p_tenant uuid) RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(
    (SELECT w.pool FROM tenant_worker_pools w WHERE w.tenant_id = p_tenant),
    (SELECT w.pool FROM tenants t JOIN tenant_worker_pools w ON w.tenant_id = t.parent_id WHERE t.id = p_tenant),
    (SELECT pw.pool FROM tenants t
       JOIN subscriptions s ON s.tenant_id = COALESCE(t.parent_id, t.id)
       JOIN plan_worker_pools pw ON pw.plan_id = s.plan_id
      WHERE t.id = p_tenant),
    'shared')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_tenant_worker_pool(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_tenant_worker_pool(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_tenant_worker_pool(uuid) TO taskiem_app;

-- Assign a tenant (p_pool NULL: remove its own assignment). The tenant
-- must be in the caller's scope, as for taskiem_set_tenant_limits; the
-- caller appends the audit entry in the same transaction.
-- +goose StatementBegin
CREATE FUNCTION taskiem_set_tenant_worker_pool(p_tenant uuid, p_pool text, p_by text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  IF p_pool IS NULL THEN
    DELETE FROM tenant_worker_pools WHERE tenant_id = p_tenant;
  ELSE
    INSERT INTO tenant_worker_pools (tenant_id, pool, assigned_by) VALUES (p_tenant, p_pool, p_by)
      ON CONFLICT (tenant_id) DO UPDATE SET pool = EXCLUDED.pool, assigned_by = EXCLUDED.assigned_by, assigned_at = now();
  END IF;
  INSERT INTO worker_pool_changes (tenant_id, pool, changed_by) VALUES (p_tenant, p_pool, p_by);
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_set_tenant_worker_pool(uuid, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_set_tenant_worker_pool(uuid, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_set_tenant_worker_pool(uuid, text, text) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_set_plan_worker_pool(p_plan text, p_pool text, p_by text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM plans WHERE id = p_plan) THEN
    RAISE EXCEPTION 'unknown plan %', p_plan USING ERRCODE = '22023';
  END IF;
  IF p_pool IS NULL THEN
    DELETE FROM plan_worker_pools WHERE plan_id = p_plan;
  ELSE
    INSERT INTO plan_worker_pools (plan_id, pool, assigned_by) VALUES (p_plan, p_pool, p_by)
      ON CONFLICT (plan_id) DO UPDATE SET pool = EXCLUDED.pool, assigned_by = EXCLUDED.assigned_by, assigned_at = now();
  END IF;
  INSERT INTO worker_pool_changes (plan_id, pool, changed_by) VALUES (p_plan, p_pool, p_by);
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_set_plan_worker_pool(text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_set_plan_worker_pool(text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_set_plan_worker_pool(text, text, text) TO taskiem_app;

-- A worker reports itself; rows unseen for a day are dropped.
-- +goose StatementBegin
CREATE FUNCTION taskiem_worker_seen(p_worker text, p_queue text, p_pool text) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM worker_pool_workers WHERE seen_at < now() - interval '1 day';
  INSERT INTO worker_pool_workers (worker_id, queue, pool) VALUES (p_worker, p_queue, p_pool)
    ON CONFLICT (worker_id, queue) DO UPDATE SET pool = EXCLUDED.pool, seen_at = now();
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_worker_seen(text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_worker_seen(text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_worker_seen(text, text, text) TO taskiem_app;

-- Queue depth by pool and queue, for metrics and `taskiem pools`: counts
-- only, no tenant ids.
-- +goose StatementBegin
CREATE FUNCTION taskiem_pool_stats()
RETURNS TABLE (pool text, queue text, ready bigint, leased bigint, oldest_ready_seconds double precision)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH per_tenant AS (
    SELECT t.tenant_id, t.queue,
           count(*) FILTER (WHERE t.lease_owner IS NULL AND t.available_at <= now()) AS ready,
           count(*) FILTER (WHERE t.lease_owner IS NOT NULL) AS leased,
           min(t.available_at) FILTER (WHERE t.lease_owner IS NULL AND t.available_at <= now()) AS oldest
      FROM tasks t GROUP BY t.tenant_id, t.queue)
  SELECT taskiem_tenant_worker_pool(p.tenant_id), p.queue, sum(p.ready)::bigint, sum(p.leased)::bigint,
         COALESCE(EXTRACT(EPOCH FROM now() - min(p.oldest)), 0)::double precision
    FROM per_tenant p GROUP BY 1, 2
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_pool_stats() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_pool_stats() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_pool_stats() TO taskiem_app;

-- Claiming takes a pool. Only tenants routed to p_pool are considered;
-- the rest is as 00080 (round-robin across tenants, each under its cap).
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval, p_tenant_cap int, p_pool text)
RETURNS TABLE (task_id uuid, tenant_id uuid, run_id uuid, step_id text, attempt int, lease_epoch bigint)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH RECURSIVE ready AS (
    (SELECT t.tenant_id FROM tasks t WHERE t.queue = p_queue AND t.lease_owner IS NULL ORDER BY t.tenant_id LIMIT 1)
    UNION ALL
    SELECT (SELECT t.tenant_id FROM tasks t WHERE t.queue = p_queue AND t.lease_owner IS NULL AND t.tenant_id > r.tenant_id ORDER BY t.tenant_id LIMIT 1)
      FROM ready r WHERE r.tenant_id IS NOT NULL),
  mine AS (
    SELECT r.tenant_id FROM ready r
     WHERE r.tenant_id IS NOT NULL AND taskiem_tenant_worker_pool(r.tenant_id) = p_pool),
  caps AS (
    SELECT r.tenant_id,
           CASE WHEN p_queue = 'container'
                THEN COALESCE(LEAST(NULLIF(l.container_concurrency, 0), NULLIF(pl.container_concurrency, 0)), p_tenant_cap)
                ELSE COALESCE(l.worker_concurrency, p_tenant_cap) END AS cap
      FROM mine r
      LEFT JOIN tenant_limits l ON l.tenant_id = r.tenant_id
      LEFT JOIN tenants tn ON tn.id = r.tenant_id
      LEFT JOIN tenant_limits pl ON pl.tenant_id = tn.parent_id AND p_queue = 'container'),
  room AS (
    SELECT c.tenant_id, CASE WHEN c.cap <= 0 THEN p_limit
      ELSE LEAST(p_limit, c.cap - (SELECT count(*) FROM tasks x
        WHERE x.tenant_id = c.tenant_id AND x.queue = p_queue AND x.lease_owner IS NOT NULL)::int) END AS n
      FROM caps c),
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
ALTER FUNCTION taskiem_claim_tasks(text, text, int, interval, int, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int, text) TO taskiem_app;

-- Workers of the previous release (five arguments) claim for the shared
-- pool only, so a rolling upgrade never hands a dedicated tenant's task to
-- a shared worker.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval DEFAULT '60 seconds', p_tenant_cap int DEFAULT 0)
RETURNS TABLE (task_id uuid, tenant_id uuid, run_id uuid, step_id text, attempt int, lease_epoch bigint)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT * FROM taskiem_claim_tasks(p_queue, p_worker, p_limit, p_lease, p_tenant_cap, 'shared')
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval DEFAULT '60 seconds', p_tenant_cap int DEFAULT 0)
RETURNS TABLE (task_id uuid, tenant_id uuid, run_id uuid, step_id text, attempt int, lease_epoch bigint)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH RECURSIVE ready AS (
    (SELECT t.tenant_id FROM tasks t WHERE t.queue = p_queue AND t.lease_owner IS NULL ORDER BY t.tenant_id LIMIT 1)
    UNION ALL
    SELECT (SELECT t.tenant_id FROM tasks t WHERE t.queue = p_queue AND t.lease_owner IS NULL AND t.tenant_id > r.tenant_id ORDER BY t.tenant_id LIMIT 1)
      FROM ready r WHERE r.tenant_id IS NOT NULL),
  caps AS (
    SELECT r.tenant_id,
           CASE WHEN p_queue = 'container'
                THEN COALESCE(LEAST(NULLIF(l.container_concurrency, 0), NULLIF(pl.container_concurrency, 0)), p_tenant_cap)
                ELSE COALESCE(l.worker_concurrency, p_tenant_cap) END AS cap
      FROM ready r
      LEFT JOIN tenant_limits l ON l.tenant_id = r.tenant_id
      LEFT JOIN tenants tn ON tn.id = r.tenant_id
      LEFT JOIN tenant_limits pl ON pl.tenant_id = tn.parent_id AND p_queue = 'container'
     WHERE r.tenant_id IS NOT NULL),
  room AS (
    SELECT c.tenant_id, CASE WHEN c.cap <= 0 THEN p_limit
      ELSE LEAST(p_limit, c.cap - (SELECT count(*) FROM tasks x
        WHERE x.tenant_id = c.tenant_id AND x.queue = p_queue AND x.lease_owner IS NOT NULL)::int) END AS n
      FROM caps c),
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
DROP FUNCTION taskiem_claim_tasks(text, text, int, interval, int, text);
DROP FUNCTION taskiem_pool_stats();
DROP FUNCTION taskiem_worker_seen(text, text, text);
DROP FUNCTION taskiem_set_plan_worker_pool(text, text, text);
DROP FUNCTION taskiem_set_tenant_worker_pool(uuid, text, text);
DROP FUNCTION taskiem_tenant_worker_pool(uuid);
DROP TABLE worker_pool_workers;
DROP TABLE worker_pool_changes;
DROP TABLE plan_worker_pools;
DROP TABLE tenant_worker_pools;
