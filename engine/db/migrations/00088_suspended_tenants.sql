-- A suspended tenant (a partner's suspended sub-tenant, or any tenant an
-- operator suspends) does no new work (docs/embedding.md, docs/governance.md):
-- its schedules are not claimed, deliveries to its webhooks and connector
-- triggers are refused (423) and counted, runs it queued are not admitted,
-- its repair jobs wait, and the engine refuses to start its runs. When it
-- is resumed, schedules continue from the resume: fires missed while it
-- was suspended are skipped, not caught up.

-- +goose Up
ALTER TABLE tenants ADD COLUMN suspended_at timestamptz, ADD COLUMN resumed_at timestamptz;

-- +goose StatementBegin
CREATE FUNCTION taskiem_tenant_status_changed() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.status IS DISTINCT FROM OLD.status THEN
    IF NEW.status = 'active' THEN
      NEW.resumed_at := now();
    ELSE
      NEW.suspended_at := now();
    END IF;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tenants_status_changed BEFORE UPDATE OF status ON tenants FOR EACH ROW EXECUTE FUNCTION taskiem_tenant_status_changed();

-- Deliveries refused because the tenant was suspended, counted per day.
CREATE TABLE ingest_refusals (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  day        date NOT NULL,
  kind       text NOT NULL CHECK (kind IN ('webhook', 'connector')),
  reason     text NOT NULL CHECK (reason IN ('suspended')),
  hits       bigint NOT NULL DEFAULT 1,
  first_at   timestamptz NOT NULL DEFAULT now(),
  last_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, day, kind, reason)
);
ALTER TABLE ingest_refusals ENABLE ROW LEVEL SECURITY;
ALTER TABLE ingest_refusals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ingest_refusals TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON ingest_refusals TO taskiem_app;

-- Schedules of active tenants only.
DROP FUNCTION taskiem_claim_due_schedules(int, int, interval);
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_due_schedules(p_limit int, p_per_tenant int DEFAULT 10, p_lease interval DEFAULT '30 seconds')
RETURNS TABLE (trigger_id uuid, tenant_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE triggers t SET lease_until = now() + p_lease
   WHERE t.id IN (
     SELECT c.id FROM triggers c
      WHERE c.id IN (
        SELECT d.id FROM (
          SELECT tr.id, row_number() OVER (PARTITION BY tr.tenant_id ORDER BY tr.next_fire_at, tr.id) AS n
            FROM triggers tr JOIN tenants te ON te.id = tr.tenant_id AND te.status = 'active'
           WHERE tr.type = 'schedule' AND tr.next_fire_at <= now() AND (tr.lease_until IS NULL OR tr.lease_until < now())) d
         WHERE d.n <= p_per_tenant)
        AND c.type = 'schedule' AND c.next_fire_at <= now() AND (c.lease_until IS NULL OR c.lease_until < now())
      ORDER BY c.next_fire_at
      LIMIT p_limit
      FOR UPDATE SKIP LOCKED)
  RETURNING t.id, t.tenant_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_due_schedules(int, int, interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_due_schedules(int, int, interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_due_schedules(int, int, interval) TO taskiem_app;

-- Backlogs of active tenants only.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_backlog_tenants(p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH RECURSIVE t AS (
    (SELECT r.tenant_id FROM runs r WHERE r.status = 'queued' AND r.queue_reason = 'tenant' ORDER BY r.tenant_id LIMIT 1)
    UNION ALL
    SELECT (SELECT r.tenant_id FROM runs r WHERE r.status = 'queued' AND r.queue_reason = 'tenant' AND r.tenant_id > t.tenant_id ORDER BY r.tenant_id LIMIT 1)
      FROM t WHERE t.tenant_id IS NOT NULL)
  SELECT t.tenant_id FROM t JOIN tenants te ON te.id = t.tenant_id AND te.status = 'active'
   ORDER BY (SELECT min(r.started_at) FROM runs r WHERE r.tenant_id = t.tenant_id AND r.status = 'queued' AND r.queue_reason = 'tenant')
   LIMIT p_limit
$$;
-- +goose StatementEnd

-- Repair jobs of active tenants only.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_repair_due_tenants(p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT j.tenant_id FROM repair_jobs j JOIN tenants te ON te.id = j.tenant_id AND te.status = 'active'
   WHERE (j.status = 'queued' AND j.available_at <= now())
      OR (j.status = 'running' AND j.started_at < now() - interval '30 minutes' AND j.attempts < 3)
   LIMIT p_limit
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_repair_due_tenants(p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT j.tenant_id FROM repair_jobs j
   WHERE (j.status = 'queued' AND j.available_at <= now())
      OR (j.status = 'running' AND j.started_at < now() - interval '30 minutes' AND j.attempts < 3)
   LIMIT p_limit
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_backlog_tenants(p_limit int)
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
DROP FUNCTION taskiem_claim_due_schedules(int, int, interval);
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_due_schedules(p_limit int, p_per_tenant int DEFAULT 10, p_lease interval DEFAULT '30 seconds')
RETURNS TABLE (trigger_id uuid, tenant_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE triggers t SET lease_until = now() + p_lease
   WHERE t.id IN (
     SELECT c.id FROM triggers c
      WHERE c.id IN (
        SELECT d.id FROM (
          SELECT id, row_number() OVER (PARTITION BY triggers.tenant_id ORDER BY next_fire_at, id) AS n
            FROM triggers
           WHERE type = 'schedule' AND next_fire_at <= now() AND (lease_until IS NULL OR lease_until < now())) d
         WHERE d.n <= p_per_tenant)
        AND c.type = 'schedule' AND c.next_fire_at <= now() AND (c.lease_until IS NULL OR c.lease_until < now())
      ORDER BY c.next_fire_at
      LIMIT p_limit
      FOR UPDATE SKIP LOCKED)
  RETURNING t.id, t.tenant_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_due_schedules(int, int, interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_due_schedules(int, int, interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_due_schedules(int, int, interval) TO taskiem_app;
DROP TABLE ingest_refusals;
DROP TRIGGER tenants_status_changed ON tenants;
DROP FUNCTION taskiem_tenant_status_changed();
ALTER TABLE tenants DROP COLUMN suspended_at, DROP COLUMN resumed_at;
