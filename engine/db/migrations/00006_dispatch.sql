-- The only cross-tenant paths (spec 5.3, decision 0004). Each function is
-- SECURITY DEFINER, owned by taskiem_dispatch, claims rows with SKIP LOCKED,
-- and returns routing columns only. Callers then narrow app.tenant_scope to
-- the returned tenant before touching any data.

-- +goose Up

-- taskiem_dispatch sees every row of the tables it routes, and nothing else.
CREATE POLICY dispatch ON tasks  TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON timers TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON runs   TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON users       FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch ON memberships FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch ON tenants     FOR SELECT TO taskiem_dispatch USING (true);

GRANT SELECT (id, tenant_id, run_id, step_id, attempt, queue, priority, available_at, lease_owner, lease_until, lease_epoch),
      UPDATE (lease_owner, lease_until, lease_epoch) ON tasks TO taskiem_dispatch;
GRANT SELECT (id, tenant_id, run_id, step_id, kind, fire_at, fired_at, lease_owner, lease_until),
      UPDATE (lease_owner, lease_until) ON timers TO taskiem_dispatch;
GRANT SELECT (id, tenant_id, started_at, last_seq, decided_seq, orch_lease_owner, orch_lease_until),
      UPDATE (orch_lease_owner, orch_lease_until) ON runs TO taskiem_dispatch;
GRANT SELECT (id, email, password_hash, status) ON users TO taskiem_dispatch;
GRANT SELECT (tenant_id, user_id, role) ON memberships TO taskiem_dispatch;
GRANT SELECT (id, parent_id, status) ON tenants TO taskiem_dispatch;

-- Workers: claim up to p_limit tasks from a queue, highest priority first.
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

-- Scheduler: claim due timers with a short lease. The caller appends
-- TimerFired and sets fired_at under the tenant's scope; an expired lease
-- makes the timer claimable again, and fired_at makes firing happen once.
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_due_timers(p_worker text, p_limit int, p_lease interval DEFAULT '30 seconds')
RETURNS TABLE (timer_id uuid, tenant_id uuid, run_id uuid, step_id text, kind text, fire_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH c AS (
    SELECT t.id FROM timers t
     WHERE t.fired_at IS NULL AND t.fire_at <= now()
       AND (t.lease_until IS NULL OR t.lease_until < now())
     ORDER BY t.fire_at
     LIMIT p_limit
     FOR UPDATE SKIP LOCKED)
  UPDATE timers t SET lease_owner = p_worker, lease_until = now() + p_lease
    FROM c WHERE t.id = c.id
  RETURNING t.id, t.tenant_id, t.run_id, t.step_id, t.kind, t.fire_at
$$;
-- +goose StatementEnd

-- Orchestrators: claim runs with undecided events. The caller then locks the
-- run row under the tenant's scope and re-checks decided_seq < last_seq, so a
-- duplicate claim after lease expiry is a no-op, never a double decision.
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_runs_to_orchestrate(p_worker text, p_limit int, p_lease interval DEFAULT '30 seconds')
RETURNS TABLE (run_id uuid, tenant_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH c AS (
    SELECT r.id FROM runs r
     WHERE r.decided_seq < r.last_seq
       AND (r.orch_lease_until IS NULL OR r.orch_lease_until < now())
     ORDER BY r.started_at
     LIMIT p_limit
     FOR UPDATE SKIP LOCKED)
  UPDATE runs r SET orch_lease_owner = p_worker, orch_lease_until = now() + p_lease
    FROM c WHERE r.id = c.id
  RETURNING r.id, r.tenant_id
$$;
-- +goose StatementEnd

-- Scheduler: release task leases that expired. lease_epoch is kept, so the
-- next claim increments it and the stale holder's writes are fenced out.
-- +goose StatementBegin
CREATE FUNCTION taskiem_recover_expired_leases(p_limit int DEFAULT 1000)
RETURNS int
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH c AS (
    SELECT t.id FROM tasks t
     WHERE t.lease_owner IS NOT NULL AND t.lease_until < now()
     LIMIT p_limit
     FOR UPDATE SKIP LOCKED),
  u AS (
    UPDATE tasks t SET lease_owner = NULL, lease_until = NULL
      FROM c WHERE t.id = c.id
    RETURNING 1)
  SELECT count(*)::int FROM u
$$;
-- +goose StatementEnd

-- API authentication: look up a login before any tenant scope exists.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_find_user(p_email text)
RETURNS TABLE (user_id uuid, password_hash text, status text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.id, u.password_hash, u.status FROM users u WHERE lower(u.email) = lower(p_email)
$$;
-- +goose StatementEnd

-- API authentication: the tenant scope for a user. Sub-tenants are included
-- only for the partner admin API (spec 5.3), and only under active parents.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_tenant_scope(p_user uuid, p_include_subtenants boolean DEFAULT false)
RETURNS uuid[]
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH own AS (
    SELECT DISTINCT m.tenant_id FROM memberships m
      JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
     WHERE m.user_id = p_user),
  subs AS (
    SELECT t.id FROM tenants t
     WHERE p_include_subtenants AND t.status = 'active' AND t.parent_id IN (SELECT tenant_id FROM own))
  SELECT COALESCE(array_agg(id ORDER BY id), '{}') FROM (
    SELECT tenant_id AS id FROM own UNION SELECT id FROM subs) s
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_claim_tasks(text, text, int, interval) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_claim_due_timers(text, int, interval) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_claim_runs_to_orchestrate(text, int, interval) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_recover_expired_leases(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_find_user(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_tenant_scope(uuid, boolean) OWNER TO taskiem_dispatch;

REVOKE EXECUTE ON FUNCTION
  taskiem_claim_tasks(text, text, int, interval),
  taskiem_claim_due_timers(text, int, interval),
  taskiem_claim_runs_to_orchestrate(text, int, interval),
  taskiem_recover_expired_leases(int),
  taskiem_auth_find_user(text),
  taskiem_auth_tenant_scope(uuid, boolean)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  taskiem_claim_tasks(text, text, int, interval),
  taskiem_claim_due_timers(text, int, interval),
  taskiem_claim_runs_to_orchestrate(text, int, interval),
  taskiem_recover_expired_leases(int),
  taskiem_auth_find_user(text),
  taskiem_auth_tenant_scope(uuid, boolean)
TO taskiem_app;
