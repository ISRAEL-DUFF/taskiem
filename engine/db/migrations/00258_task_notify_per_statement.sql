-- Task wake-ups are sent once per queue and statement, not once per row
-- (docs/performance.md, "Commit serialisation by NOTIFY"). The row trigger
-- of 00004_execution called pg_notify for every task inserted; a
-- transaction that notifies takes Postgres's notify lock at commit, and
-- every call queues a payload the commit then de-duplicates. Now a
-- statement-level trigger reads the statement's new rows and notifies each
-- distinct queue once.
--
-- A task that is not due yet (a retry's backoff) wakes nobody: a woken
-- worker would claim nothing, and workers poll every second when idle
-- (engine/runtime/worker.go), which is how such a task is found once it is
-- due. One second of slack keeps a task stamped "now" by an engine clock a
-- little ahead of the database's notifying as before.
--
-- The payload is unchanged: the queue's name, which listen() matches
-- against the worker's queue (engine/runtime/worker.go).

-- +goose Up
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
DROP TRIGGER tasks_notify ON tasks;
CREATE TRIGGER tasks_notify AFTER INSERT ON tasks
  REFERENCING NEW TABLE AS new_tasks
  FOR EACH STATEMENT EXECUTE FUNCTION taskiem_notify_task();

-- +goose Down
DROP TRIGGER tasks_notify ON tasks;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_notify_task() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('taskiem_tasks', NEW.queue);
  RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tasks_notify AFTER INSERT ON tasks
  FOR EACH ROW EXECUTE FUNCTION taskiem_notify_task();
