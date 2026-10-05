-- Concurrency control (spec 4.8, 8.3), operator resolution, retention (spec 9.4).

-- +goose Up
-- RunAdmitted: a queued run got its concurrency slot and starts now.
-- StepResolved: an operator settled a parked step after checking the provider.
ALTER TABLE run_events DROP CONSTRAINT run_events_type_check;
ALTER TABLE run_events ADD CONSTRAINT run_events_type_check CHECK (type IN (
  'RunStarted', 'RunAdmitted', 'StepScheduled', 'StepStarted', 'EffectIntent', 'StepCompleted',
  'StepFailed', 'StepSkipped', 'RetryScheduled', 'TimerFired', 'SignalReceived',
  'ApprovalRequested', 'ApprovalDecided', 'CompensationStarted', 'CompensationCompleted',
  'RunCompleted', 'RunFailed', 'RunCancelled'));

-- One row per run holding a concurrency_key slot; the unique index makes a
-- second holder impossible.
CREATE TABLE concurrency_slots (
  tenant_id    uuid NOT NULL,
  workflow_id  uuid NOT NULL,
  key          text NOT NULL,
  run_id       uuid NOT NULL REFERENCES runs(id),
  acquired_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, workflow_id, key)
);
CREATE UNIQUE INDEX concurrency_slots_run ON concurrency_slots (run_id);
CREATE INDEX runs_queued ON runs (tenant_id, workflow_id, started_at) WHERE status = 'queued';

ALTER TABLE concurrency_slots ENABLE ROW LEVEL SECURITY;
ALTER TABLE concurrency_slots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON concurrency_slots TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON concurrency_slots TO taskiem_app;

-- Retention: purge one ended run past retain_until, under the caller's
-- tenant scope. The only way events leave the append-only log.
-- +goose StatementBegin
CREATE FUNCTION taskiem_purge_run(p_run uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_ok boolean;
BEGIN
  SELECT true INTO v_ok FROM runs
   WHERE id = p_run AND tenant_id = ANY (taskiem_tenant_scope())
     AND status IN ('completed', 'failed', 'cancelled') AND retain_until IS NOT NULL AND retain_until < now()
   FOR UPDATE;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  DELETE FROM run_events WHERE run_id = p_run;
  DELETE FROM tasks WHERE run_id = p_run;
  DELETE FROM timers WHERE run_id = p_run;
  DELETE FROM signal_waits WHERE run_id = p_run;
  DELETE FROM concurrency_slots WHERE run_id = p_run;
  DELETE FROM runs WHERE id = p_run;
  RETURN true;
END
$$;
-- +goose StatementEnd
-- Owned by taskiem_dispatch, which may delete only through these policies;
-- the function itself enforces the caller's tenant scope and retention.
-- (A schema owner would see no rows: RLS is forced on every table.)
CREATE POLICY dispatch_purge ON run_events      FOR DELETE TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_purge ON signal_waits    FOR DELETE TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_purge ON concurrency_slots FOR DELETE TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_select ON run_events     FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_select ON signal_waits   FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_select ON concurrency_slots FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (run_id), DELETE ON run_events, signal_waits, concurrency_slots TO taskiem_dispatch;
GRANT SELECT (status, retain_until), DELETE ON runs TO taskiem_dispatch;
GRANT DELETE ON tasks, timers TO taskiem_dispatch;
ALTER FUNCTION taskiem_purge_run(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_purge_run(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_purge_run(uuid) TO taskiem_app;

-- Drops run_events partitions entirely before the current month that hold
-- no rows (retention deletes runs; empty months are then dropped).
-- +goose StatementBegin
CREATE FUNCTION taskiem_drop_empty_run_event_partitions()
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r record;
  v_dropped int := 0;
  v_has boolean;
BEGIN
  FOR r IN
    SELECT c.relname, pg_get_expr(c.relpartbound, c.oid) AS bound
      FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
     WHERE i.inhparent = 'run_events'::regclass
  LOOP
    IF substring(r.bound from 'TO \(''([^'']+)''\)')::timestamptz > date_trunc('month', now()) THEN
      CONTINUE;
    END IF;
    EXECUTE format('SELECT EXISTS (SELECT 1 FROM %I)', r.relname) INTO v_has;
    IF NOT v_has THEN
      EXECUTE format('DROP TABLE %I', r.relname);
      v_dropped := v_dropped + 1;
    END IF;
  END LOOP;
  RETURN v_dropped;
END
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_drop_empty_run_event_partitions() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_drop_empty_run_event_partitions() TO taskiem_app;

-- Scheduler: find ended runs past retention, across tenants (routing only).
-- +goose StatementBegin
CREATE FUNCTION taskiem_claim_purgeable_runs(p_limit int)
RETURNS TABLE (run_id uuid, tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT r.id, r.tenant_id FROM runs r
   WHERE r.retain_until IS NOT NULL AND r.retain_until < now()
     AND r.status IN ('completed', 'failed', 'cancelled')
   ORDER BY r.retain_until
   LIMIT p_limit
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_claim_purgeable_runs(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_claim_purgeable_runs(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_claim_purgeable_runs(int) TO taskiem_app;
