-- Execution: runs, events, tasks, timers, trigger receipts. Spec 4, 5.2, 8.2, 9.4.

-- +goose Up
CREATE TABLE runs (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  workflow_id       uuid NOT NULL,
  version           int  NOT NULL,
  environment       text NOT NULL,
  status            text NOT NULL DEFAULT 'running'
                    CHECK (status IN ('queued', 'running', 'waiting', 'needs_reconciliation', 'completed', 'failed', 'cancelled')),
  correlation_key   text,
  concurrency_key   text,
  parent_run_id     uuid,                          -- subflows and forks
  forked_from_seq   bigint,
  last_seq          bigint NOT NULL DEFAULT 0,     -- highest event seq; allocated under the row lock
  decided_seq       bigint NOT NULL DEFAULT 0,     -- highest seq the orchestrator has decided on
  orch_lease_owner  text,
  orch_lease_until  timestamptz,
  started_at        timestamptz NOT NULL,
  ended_at          timestamptz,
  retain_until      timestamptz,                   -- set when the run ends: ended_at + retention
  FOREIGN KEY (workflow_id, version) REFERENCES workflow_versions (workflow_id, version),
  CHECK (decided_seq <= last_seq),
  CHECK ((status IN ('completed', 'failed', 'cancelled')) = (ended_at IS NOT NULL))
);
CREATE INDEX ON runs (tenant_id, workflow_id, started_at DESC);
CREATE INDEX ON runs (tenant_id, correlation_key) WHERE correlation_key IS NOT NULL;
CREATE INDEX ON runs (retain_until) WHERE retain_until IS NOT NULL;
CREATE INDEX runs_pending_decide ON runs (started_at) WHERE decided_seq < last_seq;

-- Partitioned by the run's start time so a run's events share one partition.
-- run_started_at is constant per run, so (run_id, seq) is unique in effect.
CREATE TABLE run_events (
  run_id          uuid        NOT NULL,
  seq             bigint      NOT NULL CHECK (seq >= 1),
  run_started_at  timestamptz NOT NULL,
  tenant_id       uuid        NOT NULL,
  type            text        NOT NULL CHECK (type IN (
                    'RunStarted', 'StepScheduled', 'StepStarted', 'EffectIntent', 'StepCompleted',
                    'StepFailed', 'StepSkipped', 'RetryScheduled', 'TimerFired', 'SignalReceived',
                    'ApprovalRequested', 'ApprovalDecided', 'CompensationStarted', 'CompensationCompleted',
                    'RunCompleted', 'RunFailed', 'RunCancelled')),
  step_id         text,
  attempt         int,
  payload         jsonb,                           -- inline up to 256 KB; PII as encrypted envelopes
  payload_ref     bytea,                           -- content hash in object storage
  recorded_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (run_id, seq, run_started_at),
  CHECK (payload IS NULL OR pg_column_size(payload) <= 262144)
) PARTITION BY RANGE (run_started_at);

CREATE TABLE tasks (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL,
  run_id        uuid NOT NULL REFERENCES runs(id),
  step_id       text NOT NULL,
  attempt       int  NOT NULL CHECK (attempt >= 1),
  queue         text NOT NULL,                     -- connector | sandbox | ai | dedicated:<tenant>
  priority      smallint NOT NULL DEFAULT 5,       -- lower runs first
  available_at  timestamptz NOT NULL DEFAULT now(),
  lease_owner   text,
  lease_until   timestamptz,
  lease_epoch   bigint NOT NULL DEFAULT 0,         -- fencing token, incremented on every claim
  created_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, step_id, attempt),
  CHECK ((lease_owner IS NULL) = (lease_until IS NULL))
);
CREATE INDEX tasks_claimable ON tasks (queue, priority, available_at) WHERE lease_owner IS NULL;
CREATE INDEX tasks_leased ON tasks (lease_until) WHERE lease_owner IS NOT NULL;

CREATE TABLE timers (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL,
  run_id       uuid NOT NULL REFERENCES runs(id),
  step_id      text,
  kind         text NOT NULL CHECK (kind IN ('wait', 'step_timeout', 'retry', 'approval_timeout', 'signal_timeout', 'run_timeout')),
  fire_at      timestamptz NOT NULL,
  fired_at     timestamptz,
  lease_owner  text,
  lease_until  timestamptz
);
CREATE INDEX timers_due ON timers (fire_at) WHERE fired_at IS NULL;

CREATE TABLE trigger_receipts (
  tenant_id    uuid NOT NULL,
  trigger_id   text NOT NULL,
  dedup_key    text NOT NULL,
  run_id       uuid NOT NULL,
  received_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, trigger_id, dedup_key)
);

-- Wake idle workers as soon as a task is available (spec 4.3).
-- +goose StatementBegin
CREATE FUNCTION taskiem_notify_task() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('taskiem_tasks', NEW.queue);
  RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tasks_notify AFTER INSERT ON tasks
  FOR EACH ROW EXECUTE FUNCTION taskiem_notify_task();

-- Appends one event, allocating seq under the run row lock. Runs as the
-- caller, so RLS decides whether the run is visible at all.
-- +goose StatementBegin
CREATE FUNCTION taskiem_append_event(p_run_id uuid, p_type text, p_step_id text, p_attempt int, p_payload jsonb)
RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE
  v_seq bigint;
  v_tenant uuid;
  v_started timestamptz;
BEGIN
  UPDATE runs SET last_seq = last_seq + 1
   WHERE id = p_run_id
  RETURNING last_seq, tenant_id, started_at INTO v_seq, v_tenant, v_started;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'run % not found in tenant scope', p_run_id USING ERRCODE = 'no_data_found';
  END IF;
  INSERT INTO run_events (run_id, seq, run_started_at, tenant_id, type, step_id, attempt, payload)
  VALUES (p_run_id, v_seq, v_started, v_tenant, p_type, p_step_id, p_attempt, p_payload);
  RETURN v_seq;
END
$$;
-- +goose StatementEnd

-- Fencing (spec 4.3): every worker write for a task runs one of these in the
-- same transaction and aborts if it returns false.
-- +goose StatementBegin
CREATE FUNCTION taskiem_task_heartbeat(p_task uuid, p_worker text, p_epoch bigint, p_extend interval)
RETURNS boolean
LANGUAGE sql AS $$
  WITH u AS (
    UPDATE tasks SET lease_until = now() + p_extend
     WHERE id = p_task AND lease_owner = p_worker AND lease_epoch = p_epoch
    RETURNING 1)
  SELECT count(*) = 1 FROM u
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_task_finish(p_task uuid, p_worker text, p_epoch bigint)
RETURNS boolean
LANGUAGE sql AS $$
  WITH d AS (
    DELETE FROM tasks
     WHERE id = p_task AND lease_owner = p_worker AND lease_epoch = p_epoch
    RETURNING 1)
  SELECT count(*) = 1 FROM d
$$;
-- +goose StatementEnd

-- Creates monthly run_events partitions from p_from's month for p_months months.
-- Called by migrations and by the scheduler's maintenance job.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ensure_run_event_partitions(p_from timestamptz, p_months int)
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_start date := date_trunc('month', p_from AT TIME ZONE 'UTC')::date;
  v_created int := 0;
  v_name text;
BEGIN
  FOR i IN 0 .. p_months - 1 LOOP
    v_name := format('run_events_%s', to_char(v_start + make_interval(months => i), 'YYYY_MM'));
    IF to_regclass(v_name) IS NULL THEN
      EXECUTE format(
        'CREATE TABLE %I PARTITION OF run_events FOR VALUES FROM (%L) TO (%L)',
        v_name,
        (v_start + make_interval(months => i))::timestamp AT TIME ZONE 'UTC',
        (v_start + make_interval(months => i + 1))::timestamp AT TIME ZONE 'UTC');
      v_created := v_created + 1;
    END IF;
  END LOOP;
  RETURN v_created;
END
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_ensure_run_event_partitions(timestamptz, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ensure_run_event_partitions(timestamptz, int) TO taskiem_app;
SELECT taskiem_ensure_run_event_partitions(now() - interval '1 month', 4);

-- RLS
ALTER TABLE runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE runs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON runs TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

ALTER TABLE run_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE run_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON run_events TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

ALTER TABLE tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tasks TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

ALTER TABLE timers ENABLE ROW LEVEL SECURITY;
ALTER TABLE timers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON timers TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

ALTER TABLE trigger_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE trigger_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON trigger_receipts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

-- run_events is append-only (spec 5.3).
GRANT SELECT, INSERT ON run_events TO taskiem_app;
GRANT SELECT, INSERT, UPDATE ON runs TO taskiem_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tasks, timers TO taskiem_app;
GRANT SELECT, INSERT ON trigger_receipts TO taskiem_app;
