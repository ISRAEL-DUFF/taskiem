-- Read replicas (Phase 4 P4-3, decision 0024). Processes with
-- TASKIEM_DATABASE_READ_URL measure how far the replica is behind by
-- writing the time to this one row on the primary and reading it back from
-- the replica: lag = the replica's now() minus the newest beat it has
-- replayed. It needs no privileges on the standby and is true end to end
-- (an idle primary does not look like a lagging replica). No tenant data.

-- +goose Up
CREATE TABLE db_heartbeat (
  id       int PRIMARY KEY CHECK (id = 1),
  beat_at  timestamptz NOT NULL
);
INSERT INTO db_heartbeat VALUES (1, now());
ALTER TABLE db_heartbeat ENABLE ROW LEVEL SECURITY;
ALTER TABLE db_heartbeat FORCE ROW LEVEL SECURITY;
CREATE POLICY heartbeat ON db_heartbeat TO taskiem_app USING (id = 1) WITH CHECK (id = 1);
GRANT SELECT, UPDATE (beat_at) ON db_heartbeat TO taskiem_app;

-- +goose Down
DROP TABLE db_heartbeat;
