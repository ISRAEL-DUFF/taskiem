-- The container queue is served by the shared container worker pool alone
-- (docs/container-steps.md, docs/cloud.md#dedicated-worker-pools): there is
-- no dedicated container capacity, and the chart refuses a dedicated pool
-- that names the queue. Claiming filtered every queue by the tenant's
-- routing, so a tenant routed to a dedicated pool had its container steps
-- skipped by the shared container workers and claimed by nobody: its runs
-- waited there for ever.
--
-- Now a container task belongs to the shared pool whoever's it is: the
-- claim treats it so, and queue depth reports it so (pool "shared"), so
-- the metric names the pool that must take it. Other queues are routed as
-- before (decision 0024). Signatures, owners and grants are unchanged.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval, p_tenant_cap int, p_pool text)
RETURNS TABLE (task_id uuid, tenant_id uuid, run_id uuid, step_id text, attempt int, lease_epoch bigint, ready_seconds float8)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH RECURSIVE ready AS (
    (SELECT t.tenant_id FROM tasks t WHERE t.queue = p_queue AND t.lease_owner IS NULL ORDER BY t.tenant_id LIMIT 1)
    UNION ALL
    SELECT (SELECT t.tenant_id FROM tasks t WHERE t.queue = p_queue AND t.lease_owner IS NULL AND t.tenant_id > r.tenant_id ORDER BY t.tenant_id LIMIT 1)
      FROM ready r WHERE r.tenant_id IS NOT NULL),
  mine AS (
    SELECT r.tenant_id FROM ready r
     WHERE r.tenant_id IS NOT NULL
       AND CASE WHEN p_queue = 'container' THEN 'shared' ELSE taskiem_tenant_worker_pool(r.tenant_id) END = p_pool),
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
  RETURNING t.id, t.tenant_id, t.run_id, t.step_id, t.attempt, t.lease_epoch,
            GREATEST(extract(epoch FROM clock_timestamp() - t.available_at), 0)::float8
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_pool_stats()
RETURNS TABLE (pool text, queue text, ready bigint, leased bigint, oldest_ready_seconds double precision)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH per_tenant AS (
    SELECT t.tenant_id, t.queue,
           count(*) FILTER (WHERE t.lease_owner IS NULL AND t.available_at <= now()) AS ready,
           count(*) FILTER (WHERE t.lease_owner IS NOT NULL) AS leased,
           min(t.available_at) FILTER (WHERE t.lease_owner IS NULL AND t.available_at <= now()) AS oldest
      FROM tasks t GROUP BY t.tenant_id, t.queue)
  SELECT CASE WHEN p.queue = 'container' THEN 'shared' ELSE taskiem_tenant_worker_pool(p.tenant_id) END, p.queue, sum(p.ready)::bigint, sum(p.leased)::bigint,
         COALESCE(EXTRACT(EPOCH FROM now() - min(p.oldest)), 0)::double precision
    FROM per_tenant p GROUP BY 1, 2
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval, p_tenant_cap int, p_pool text)
RETURNS TABLE (task_id uuid, tenant_id uuid, run_id uuid, step_id text, attempt int, lease_epoch bigint, ready_seconds float8)
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
  RETURNING t.id, t.tenant_id, t.run_id, t.step_id, t.attempt, t.lease_epoch,
            GREATEST(extract(epoch FROM clock_timestamp() - t.available_at), 0)::float8
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_pool_stats()
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
