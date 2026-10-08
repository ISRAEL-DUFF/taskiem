-- Container steps (spec 7.5, docs/container-steps.md): heavy workloads in a
-- pinned image, run per attempt in the container sandbox by the workers of
-- the "container" queue. Enabled per plan:
--
--   container_minutes_monthly  container-step minutes per UTC month; 0 (the
--                              platform default) turns container steps off
--   container_concurrency      the tenant's container steps running at once
--                              (null or 0: the platform default,
--                              TASKIEM_DEFAULT_CONTAINER_CONCURRENCY)
--
-- Both are set with taskiem_set_tenant_limits like any limit (it takes any
-- column since 00063); a sub-tenant inherits its partner's and can only be
-- lowered below them. container_usage counts what each tenant's steps ran
-- per month, in seconds, from the times the sandbox (not the program)
-- reports.

-- +goose Up
ALTER TABLE tenant_limits
  ADD COLUMN container_minutes_monthly bigint CHECK (container_minutes_monthly >= 0),
  ADD COLUMN container_concurrency int CHECK (container_concurrency >= 0);

CREATE TABLE container_usage (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  month       date NOT NULL CHECK (month = date_trunc('month', month)::date),
  seconds     bigint NOT NULL DEFAULT 0 CHECK (seconds >= 0),
  runs        bigint NOT NULL DEFAULT 0 CHECK (runs >= 0),
  refused     bigint NOT NULL DEFAULT 0 CHECK (refused >= 0), -- steps refused: off for the plan, or minutes used up
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, month)
);
ALTER TABLE container_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE container_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON container_usage TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON container_usage TO taskiem_app;

-- Claiming from the container queue caps each tenant at its
-- container_concurrency (the lower of its own and its partner's), not at
-- worker_concurrency: container steps hold far more than a connector call.
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
DROP TABLE container_usage;
ALTER TABLE tenant_limits DROP COLUMN container_concurrency, DROP COLUMN container_minutes_monthly;
