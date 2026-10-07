-- Reliability (Phase 4, P4-4; decision 0023).
--
-- 1. Claiming tasks also returns how long each task waited ready in the
--    queue, measured on the database clock (ready_seconds), so workers can
--    report step dispatch delay (spec 15.3: p95 under 50 ms) without clock
--    skew between pods and the database. The columns before it are
--    unchanged, so a binary one release older still reads the ones it names.
--
-- 2. taskiem_release_leases hands back every task, timer and orchestration
--    lease one process holds, at once, when it shuts down after draining
--    (spec 15.4: finish or release leases). It is what an expired lease
--    does, only sooner: lease_epoch is kept, so the next claim increments it
--    and anything the old holder still tries to write is fenced out.
--
-- Both touch routing columns only (decision 0004). This applies on top of
-- the worker pools (00120): the pool-aware claim and the five-argument
-- wrapper older workers call both return ready_seconds.

-- +goose Up
DROP FUNCTION taskiem_claim_tasks(text, text, int, interval, int);
DROP FUNCTION taskiem_claim_tasks(text, text, int, interval, int, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval, p_tenant_cap int, p_pool text)
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
ALTER FUNCTION taskiem_claim_tasks(text, text, int, interval, int, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int, text) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval DEFAULT '60 seconds', p_tenant_cap int DEFAULT 0)
RETURNS TABLE (task_id uuid, tenant_id uuid, run_id uuid, step_id text, attempt int, lease_epoch bigint, ready_seconds float8)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT * FROM taskiem_claim_tasks(p_queue, p_worker, p_limit, p_lease, p_tenant_cap, 'shared')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_tasks(text, text, int, interval, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_release_leases(p_owner text)
RETURNS int
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH t AS (
    UPDATE tasks SET lease_owner = NULL, lease_until = NULL
     WHERE lease_owner = p_owner
    RETURNING 1),
  tm AS (
    UPDATE timers SET lease_owner = NULL, lease_until = NULL
     WHERE lease_owner = p_owner AND fired_at IS NULL
    RETURNING 1),
  r AS (
    UPDATE runs SET orch_lease_owner = NULL, orch_lease_until = NULL
     WHERE orch_lease_owner = p_owner
    RETURNING 1)
  SELECT ((SELECT count(*) FROM t) + (SELECT count(*) FROM tm) + (SELECT count(*) FROM r))::int
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_release_leases(text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_release_leases(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_release_leases(text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_release_leases(text);
DROP FUNCTION taskiem_claim_tasks(text, text, int, interval, int);
DROP FUNCTION taskiem_claim_tasks(text, text, int, interval, int, text);
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

-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_tasks(p_queue text, p_worker text, p_limit int, p_lease interval DEFAULT '60 seconds', p_tenant_cap int DEFAULT 0)
RETURNS TABLE (task_id uuid, tenant_id uuid, run_id uuid, step_id text, attempt int, lease_epoch bigint)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT * FROM taskiem_claim_tasks(p_queue, p_worker, p_limit, p_lease, p_tenant_cap, 'shared')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_tasks(text, text, int, interval, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_tasks(text, text, int, interval, int) TO taskiem_app;
