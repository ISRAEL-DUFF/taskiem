-- The task trigger stays silent in a transaction whose wake-up the engine
-- sends itself (docs/spec/task-wake-ups.md, decision 0067). A transaction
-- that notifies takes Postgres's notify lock at commit, so transactions
-- that schedule tasks committed one at a time and could not share a WAL
-- flush. The engine now sends the wake-up after its commit, from one
-- notifier in each process, and says so by setting app.task_wake to
-- 'engine' for the transaction (engine/runtime/wake.go).
--
-- Any other insert notifies as before: one wake-up per queue and
-- statement, none for a task not due within a second (00258). A process on
-- an older binary never sets the value, so this migration is safe to apply
-- before the binary that uses it.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_notify_task() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_queue text;
BEGIN
  IF current_setting('app.task_wake', true) = 'engine' THEN
    RETURN NULL;
  END IF;
  FOR v_queue IN
    SELECT DISTINCT n.queue FROM new_tasks n WHERE n.available_at <= clock_timestamp() + interval '1 second'
  LOOP
    PERFORM pg_notify('taskiem_tasks', v_queue);
  END LOOP;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_notify_task() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  v_queue text;
BEGIN
  FOR v_queue IN
    SELECT DISTINCT n.queue FROM new_tasks n WHERE n.available_at <= clock_timestamp() + interval '1 second'
  LOOP
    PERFORM pg_notify('taskiem_tasks', v_queue);
  END LOOP;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
