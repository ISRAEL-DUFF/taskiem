-- A task whose outcome could not be recorded was delivered for ever: the
-- lease expired, the task was claimed again, a read ran again and an
-- idempotent write was sent again, with nothing to stop it. Two things
-- change here; the worker does the rest (Worker.finish).
--
-- The history's payload column refused anything over 256 KB as stored,
-- while a container step may return up to 1 MB (spec 4.9) and a 256 KB
-- http body is larger than that once stored. The column now takes 4 MB,
-- which holds what every step may return. The check is added NOT VALID so
-- that the table is not read through under its lock: every row already
-- there passed the stricter check.
--
-- tasks.unrecorded counts the deliveries of a task whose outcome the
-- database refused. At the third the worker settles the step with a
-- failure it can record.
-- +goose Up
ALTER TABLE run_events DROP CONSTRAINT run_events_payload_check;
ALTER TABLE run_events ADD CONSTRAINT run_events_payload_check CHECK (payload IS NULL OR pg_column_size(payload) <= 4194304) NOT VALID;
ALTER TABLE tasks ADD COLUMN unrecorded int NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE tasks DROP COLUMN unrecorded;
ALTER TABLE run_events DROP CONSTRAINT run_events_payload_check;
ALTER TABLE run_events ADD CONSTRAINT run_events_payload_check CHECK (payload IS NULL OR pg_column_size(payload) <= 262144) NOT VALID;
