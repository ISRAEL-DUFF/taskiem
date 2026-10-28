-- AI steps' budget reservations (decision 0050): an ai step's call counts
-- against the monthly AI budget only once its ai_interactions row is
-- written, after the model answers, so steps running at once all saw the
-- same budget left. Before it calls, a step now reserves an estimate of
-- the tokens it may use, under a lock per tenant, and the check counts
-- the reservations of the steps still calling. A reservation is removed
-- when the step is done, and lapses at expires_at if its worker dies.

-- +goose Up
CREATE TABLE ai_reservations (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  run_id      uuid NOT NULL,
  step_id     text NOT NULL,
  tokens      bigint NOT NULL,
  expires_at  timestamptz NOT NULL
);
CREATE INDEX ai_reservations_tenant ON ai_reservations (tenant_id, expires_at);
ALTER TABLE ai_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ai_reservations TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON ai_reservations TO taskiem_app;

-- +goose Down
DROP TABLE ai_reservations;
