-- Engine runtime: event origins, signal waits and buffered signals (spec 4.6).

-- +goose Up
-- Which component wrote each event. The determinism suite replays decide()
-- and must reproduce exactly the events with origin 'decide'.
ALTER TABLE run_events ADD COLUMN origin text NOT NULL DEFAULT 'decide'
  CHECK (origin IN ('decide', 'worker', 'ingest', 'scheduler', 'api', 'signal'));

-- +goose StatementBegin
CREATE FUNCTION taskiem_append_event(p_run_id uuid, p_type text, p_step_id text, p_attempt int, p_payload jsonb, p_origin text)
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
  INSERT INTO run_events (run_id, seq, run_started_at, tenant_id, type, step_id, attempt, payload, origin)
  VALUES (p_run_id, v_seq, v_started, v_tenant, p_type, p_step_id, p_attempt, p_payload, p_origin);
  RETURN v_seq;
END
$$;
-- +goose StatementEnd

-- A step waiting for an external event, matched by (tenant, event, correlation).
CREATE TABLE signal_waits (
  tenant_id    uuid NOT NULL,
  run_id       uuid NOT NULL REFERENCES runs(id),
  step_id      text NOT NULL,
  event        text NOT NULL,
  correlation  text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (run_id, step_id)
);
CREATE INDEX signal_waits_match ON signal_waits (tenant_id, event, correlation);

-- A signal that arrived before anything waited for it is buffered, never
-- dropped (spec 4.6), until it is consumed or expires.
CREATE TABLE signals (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL,
  event        text NOT NULL,
  correlation  text NOT NULL,
  payload      jsonb,
  received_at  timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL
);
CREATE INDEX signals_match ON signals (tenant_id, event, correlation, received_at);

ALTER TABLE signal_waits ENABLE ROW LEVEL SECURITY;
ALTER TABLE signal_waits FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON signal_waits TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

ALTER TABLE signals ENABLE ROW LEVEL SECURITY;
ALTER TABLE signals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON signals TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

GRANT SELECT, INSERT, DELETE ON signal_waits, signals TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_append_event(uuid, text, text, int, jsonb, text) TO taskiem_app;
