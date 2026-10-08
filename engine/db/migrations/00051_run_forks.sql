-- Fork from step (spec 4.10, decision 0016): a failed run resumed on a
-- (usually newer) version is a new run, linked to the one it continues
-- (parent_run_id), that replays the parent's recorded outcomes instead of
-- executing them again. run_replays holds those outcomes, copied from the
-- parent's history as recorded (personal data stays sealed); each is used
-- at most once, when the new run schedules the same step instance with the
-- same input. The fork keeps the parent's idempotency seed, so any effect
-- it sends carries the key the parent would have used (spec 4.4).

-- +goose Up
ALTER TABLE runs ADD COLUMN idempotency_seed text;
ALTER TABLE runs ADD COLUMN resumed_step text;      -- the parent's failed step, for display
CREATE INDEX runs_parent ON runs (parent_run_id) WHERE parent_run_id IS NOT NULL;

CREATE TABLE run_replays (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  run_id        uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
  step_id       text NOT NULL,                     -- step instance id
  kind          text NOT NULL CHECK (kind IN ('task', 'signal', 'timer', 'key_group')),
  payload       jsonb,                             -- the parent's StepCompleted / SignalReceived payload, as recorded
  input_digest  text,                              -- sha256 of the parent's resolved input (tasks)
  write         boolean NOT NULL DEFAULT false,    -- the parent's step was a write
  attempt_group int NOT NULL DEFAULT 0,            -- key_group: the first attempt group the fork may use
  used_at       timestamptz,
  PRIMARY KEY (run_id, step_id, kind)
);
ALTER TABLE run_replays ENABLE ROW LEVEL SECURITY;
ALTER TABLE run_replays FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON run_replays TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON run_replays TO taskiem_app;

-- +goose Down
DROP TABLE run_replays;
DROP INDEX runs_parent;
ALTER TABLE runs DROP COLUMN resumed_step;
ALTER TABLE runs DROP COLUMN idempotency_seed;
