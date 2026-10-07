-- The public status page (Phase 4, P4-4; decision 0023, docs/reliability.md).
--
-- Platform data, not tenant data: no row names a tenant, and every column
-- is shown publicly or is the operator's name. Operators declare incidents
-- and maintenance windows (taskiem status, or the status admin API with an
-- operator token); each change is an update appended to its incident, so
-- the updates are the audit trail: neither table can be changed or deleted
-- from, even by the superuser (the triggers below). A correction is a new
-- update.
--
-- status_probes keeps the synthetic canary's recent results (a week), so
-- every API replica shows the same automatic signal.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_status_components_ok(c text[])
RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
  SELECT cardinality(c) BETWEEN 1 AND 20 AND NOT EXISTS (
    SELECT 1 FROM unnest(c) x
     WHERE x NOT IN ('api', 'webhooks', 'runs', 'scheduler') AND x !~ '^integration:[a-z][a-z0-9_]{0,39}$')
$$;
-- +goose StatementEnd

CREATE TABLE status_incidents (
  id               uuid PRIMARY KEY,
  kind             text NOT NULL CHECK (kind IN ('incident', 'maintenance')),
  title            text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  components       text[] NOT NULL CHECK (taskiem_status_components_ok(components)),
  -- Maintenance windows: when the work is planned. Null for incidents.
  scheduled_start  timestamptz,
  scheduled_end    timestamptz,
  created_by       text NOT NULL CHECK (length(created_by) BETWEEN 1 AND 200),
  created_at       timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'maintenance') = (scheduled_start IS NOT NULL AND scheduled_end IS NOT NULL)),
  CHECK (scheduled_end IS NULL OR scheduled_end > scheduled_start)
);

CREATE TABLE status_updates (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  incident_id  uuid NOT NULL REFERENCES status_incidents(id),
  status       text NOT NULL CHECK (status IN ('investigating', 'identified', 'monitoring', 'resolved', 'scheduled', 'in_progress', 'completed')),
  impact       text NOT NULL CHECK (impact IN ('none', 'degraded', 'partial_outage', 'major_outage', 'maintenance')),
  message      text NOT NULL CHECK (length(message) BETWEEN 1 AND 5000),
  actor        text NOT NULL CHECK (length(actor) BETWEEN 1 AND 200),
  at           timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX status_updates_incident ON status_updates (incident_id, id);
CREATE INDEX status_incidents_created ON status_incidents (created_at DESC);

CREATE TABLE status_probes (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  at           timestamptz NOT NULL DEFAULT now(),
  ok           boolean NOT NULL,
  accept_ms    int CHECK (accept_ms >= 0),
  complete_ms  int CHECK (complete_ms >= 0),
  error        text CHECK (length(error) <= 80)
);
CREATE INDEX status_probes_at ON status_probes (at DESC);

-- +goose StatementBegin
CREATE FUNCTION taskiem_status_append_only()
RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'status incidents and updates are append-only: post a new update instead';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER append_only BEFORE UPDATE OR DELETE ON status_incidents FOR EACH ROW EXECUTE FUNCTION taskiem_status_append_only();
CREATE TRIGGER append_only BEFORE UPDATE OR DELETE ON status_updates FOR EACH ROW EXECUTE FUNCTION taskiem_status_append_only();
CREATE TRIGGER append_only_truncate BEFORE TRUNCATE ON status_incidents FOR EACH STATEMENT EXECUTE FUNCTION taskiem_status_append_only();
CREATE TRIGGER append_only_truncate BEFORE TRUNCATE ON status_updates FOR EACH STATEMENT EXECUTE FUNCTION taskiem_status_append_only();

ALTER TABLE status_incidents ENABLE ROW LEVEL SECURITY;
ALTER TABLE status_incidents FORCE ROW LEVEL SECURITY;
ALTER TABLE status_updates ENABLE ROW LEVEL SECURITY;
ALTER TABLE status_updates FORCE ROW LEVEL SECURITY;
ALTER TABLE status_probes ENABLE ROW LEVEL SECURITY;
ALTER TABLE status_probes FORCE ROW LEVEL SECURITY;
-- Public by design: readable by the application, written only through the
-- definer functions below.
CREATE POLICY public_status ON status_incidents FOR SELECT TO taskiem_app USING (true);
CREATE POLICY public_status ON status_updates FOR SELECT TO taskiem_app USING (true);
CREATE POLICY public_status ON status_probes FOR SELECT TO taskiem_app USING (true);
GRANT SELECT ON status_incidents, status_updates, status_probes TO taskiem_app;

-- An update's status must suit its kind, and nothing follows the last one.
-- +goose StatementBegin
CREATE FUNCTION taskiem_status_update(p_incident uuid, p_status text, p_impact text, p_message text, p_actor text)
RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_kind text;
  v_last text;
  v_id bigint;
BEGIN
  SELECT kind INTO v_kind FROM status_incidents WHERE id = p_incident FOR SHARE;
  IF v_kind IS NULL THEN
    RAISE EXCEPTION 'no such status incident' USING ERRCODE = 'P0002';
  END IF;
  SELECT status INTO v_last FROM status_updates WHERE incident_id = p_incident ORDER BY id DESC LIMIT 1;
  IF v_last IN ('resolved', 'completed') THEN
    RAISE EXCEPTION 'this % is closed; open a new one', v_kind USING ERRCODE = '22023';
  END IF;
  IF (v_kind = 'incident' AND p_status NOT IN ('investigating', 'identified', 'monitoring', 'resolved'))
     OR (v_kind = 'maintenance' AND p_status NOT IN ('scheduled', 'in_progress', 'completed')) THEN
    RAISE EXCEPTION 'status % does not apply to a %', p_status, v_kind USING ERRCODE = '22023';
  END IF;
  IF (v_kind = 'maintenance') <> (p_impact = 'maintenance' OR p_status = 'completed' AND p_impact = 'none') THEN
    RAISE EXCEPTION 'impact % does not apply to a %', p_impact, v_kind USING ERRCODE = '22023';
  END IF;
  INSERT INTO status_updates (incident_id, status, impact, message, actor)
  VALUES (p_incident, p_status, p_impact, p_message, p_actor) RETURNING id INTO v_id;
  RETURN v_id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_status_open(p_kind text, p_title text, p_components text[], p_start timestamptz, p_end timestamptz,
                                    p_status text, p_impact text, p_message text, p_actor text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_id uuid := gen_random_uuid();
BEGIN
  INSERT INTO status_incidents (id, kind, title, components, scheduled_start, scheduled_end, created_by)
  VALUES (v_id, p_kind, p_title, p_components, p_start, p_end, p_actor);
  PERFORM taskiem_status_update(v_id, p_status, p_impact, p_message, p_actor);
  RETURN v_id;
END
$$;
-- +goose StatementEnd

-- The canary records each probe; results older than a week are dropped.
-- +goose StatementBegin
CREATE FUNCTION taskiem_status_record_probe(p_ok boolean, p_accept_ms int, p_complete_ms int, p_error text)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO status_probes (ok, accept_ms, complete_ms, error) VALUES (p_ok, p_accept_ms, p_complete_ms, left(p_error, 80));
  DELETE FROM status_probes WHERE at < now() - interval '7 days';
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION taskiem_status_update(uuid, text, text, text, text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION taskiem_status_open(text, text, text[], timestamptz, timestamptz, text, text, text, text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION taskiem_status_record_probe(boolean, int, int, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_status_update(uuid, text, text, text, text) TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_status_open(text, text, text[], timestamptz, timestamptz, text, text, text, text) TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_status_record_probe(boolean, int, int, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_status_record_probe(boolean, int, int, text);
DROP FUNCTION taskiem_status_open(text, text, text[], timestamptz, timestamptz, text, text, text, text);
DROP FUNCTION taskiem_status_update(uuid, text, text, text, text);
DROP TABLE status_probes;
DROP TABLE status_updates;
DROP TABLE status_incidents;
DROP FUNCTION taskiem_status_append_only();
DROP FUNCTION taskiem_status_components_ok(text[]);
