-- Per-tenant monthly AI budget (spec 12.3, 16): tokens a tenant's AI
-- builds may use per UTC month. NULL takes the platform default
-- (TASKIEM_DEFAULT_AI_MONTHLY_TOKENS); operators override it with
-- `taskiem tenants limits TENANT --set ai_monthly_tokens=N`.

-- +goose Up
ALTER TABLE tenant_limits ADD COLUMN ai_monthly_tokens bigint CHECK (ai_monthly_tokens >= 0);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_set_tenant_limits(p_tenant uuid, p_values jsonb, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  k text;
  known text[] := ARRAY['ingest_rate', 'ingest_burst', 'ingest_ceiling', 'ingest_ceiling_burst', 'max_running_runs', 'max_queued_runs',
    'runs_per_day', 'runs_per_month', 'max_workflows', 'max_steps_per_run', 'worker_concurrency', 'max_payload_bytes', 'max_secrets', 'max_connections',
    'ai_monthly_tokens'];
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  FOR k IN SELECT jsonb_object_keys(p_values) LOOP
    IF NOT (k = ANY (known)) THEN
      RAISE EXCEPTION 'unknown limit %', k USING ERRCODE = '22023';
    END IF;
  END LOOP;
  INSERT INTO tenant_limits (tenant_id, updated_by) VALUES (p_tenant, p_by)
    ON CONFLICT (tenant_id) DO UPDATE SET updated_by = EXCLUDED.updated_by, updated_at = now();
  UPDATE tenant_limits l SET
    ingest_rate          = CASE WHEN p_values ? 'ingest_rate'          THEN (p_values->>'ingest_rate')::double precision ELSE l.ingest_rate END,
    ingest_burst         = CASE WHEN p_values ? 'ingest_burst'         THEN (p_values->>'ingest_burst')::int ELSE l.ingest_burst END,
    ingest_ceiling       = CASE WHEN p_values ? 'ingest_ceiling'       THEN (p_values->>'ingest_ceiling')::double precision ELSE l.ingest_ceiling END,
    ingest_ceiling_burst = CASE WHEN p_values ? 'ingest_ceiling_burst' THEN (p_values->>'ingest_ceiling_burst')::int ELSE l.ingest_ceiling_burst END,
    max_running_runs     = CASE WHEN p_values ? 'max_running_runs'     THEN (p_values->>'max_running_runs')::int ELSE l.max_running_runs END,
    max_queued_runs      = CASE WHEN p_values ? 'max_queued_runs'      THEN (p_values->>'max_queued_runs')::int ELSE l.max_queued_runs END,
    runs_per_day         = CASE WHEN p_values ? 'runs_per_day'         THEN (p_values->>'runs_per_day')::bigint ELSE l.runs_per_day END,
    runs_per_month       = CASE WHEN p_values ? 'runs_per_month'       THEN (p_values->>'runs_per_month')::bigint ELSE l.runs_per_month END,
    max_workflows        = CASE WHEN p_values ? 'max_workflows'        THEN (p_values->>'max_workflows')::int ELSE l.max_workflows END,
    max_steps_per_run    = CASE WHEN p_values ? 'max_steps_per_run'    THEN (p_values->>'max_steps_per_run')::int ELSE l.max_steps_per_run END,
    worker_concurrency   = CASE WHEN p_values ? 'worker_concurrency'   THEN (p_values->>'worker_concurrency')::int ELSE l.worker_concurrency END,
    max_payload_bytes    = CASE WHEN p_values ? 'max_payload_bytes'    THEN (p_values->>'max_payload_bytes')::int ELSE l.max_payload_bytes END,
    max_secrets          = CASE WHEN p_values ? 'max_secrets'          THEN (p_values->>'max_secrets')::int ELSE l.max_secrets END,
    max_connections      = CASE WHEN p_values ? 'max_connections'      THEN (p_values->>'max_connections')::int ELSE l.max_connections END,
    ai_monthly_tokens    = CASE WHEN p_values ? 'ai_monthly_tokens'    THEN (p_values->>'ai_monthly_tokens')::bigint ELSE l.ai_monthly_tokens END
  WHERE l.tenant_id = p_tenant;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_set_tenant_limits(p_tenant uuid, p_values jsonb, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  k text;
  known text[] := ARRAY['ingest_rate', 'ingest_burst', 'ingest_ceiling', 'ingest_ceiling_burst', 'max_running_runs', 'max_queued_runs',
    'runs_per_day', 'runs_per_month', 'max_workflows', 'max_steps_per_run', 'worker_concurrency', 'max_payload_bytes', 'max_secrets', 'max_connections'];
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  FOR k IN SELECT jsonb_object_keys(p_values) LOOP
    IF NOT (k = ANY (known)) THEN
      RAISE EXCEPTION 'unknown limit %', k USING ERRCODE = '22023';
    END IF;
  END LOOP;
  INSERT INTO tenant_limits (tenant_id, updated_by) VALUES (p_tenant, p_by)
    ON CONFLICT (tenant_id) DO UPDATE SET updated_by = EXCLUDED.updated_by, updated_at = now();
  UPDATE tenant_limits l SET
    ingest_rate          = CASE WHEN p_values ? 'ingest_rate'          THEN (p_values->>'ingest_rate')::double precision ELSE l.ingest_rate END,
    ingest_burst         = CASE WHEN p_values ? 'ingest_burst'         THEN (p_values->>'ingest_burst')::int ELSE l.ingest_burst END,
    ingest_ceiling       = CASE WHEN p_values ? 'ingest_ceiling'       THEN (p_values->>'ingest_ceiling')::double precision ELSE l.ingest_ceiling END,
    ingest_ceiling_burst = CASE WHEN p_values ? 'ingest_ceiling_burst' THEN (p_values->>'ingest_ceiling_burst')::int ELSE l.ingest_ceiling_burst END,
    max_running_runs     = CASE WHEN p_values ? 'max_running_runs'     THEN (p_values->>'max_running_runs')::int ELSE l.max_running_runs END,
    max_queued_runs      = CASE WHEN p_values ? 'max_queued_runs'      THEN (p_values->>'max_queued_runs')::int ELSE l.max_queued_runs END,
    runs_per_day         = CASE WHEN p_values ? 'runs_per_day'         THEN (p_values->>'runs_per_day')::bigint ELSE l.runs_per_day END,
    runs_per_month       = CASE WHEN p_values ? 'runs_per_month'       THEN (p_values->>'runs_per_month')::bigint ELSE l.runs_per_month END,
    max_workflows        = CASE WHEN p_values ? 'max_workflows'        THEN (p_values->>'max_workflows')::int ELSE l.max_workflows END,
    max_steps_per_run    = CASE WHEN p_values ? 'max_steps_per_run'    THEN (p_values->>'max_steps_per_run')::int ELSE l.max_steps_per_run END,
    worker_concurrency   = CASE WHEN p_values ? 'worker_concurrency'   THEN (p_values->>'worker_concurrency')::int ELSE l.worker_concurrency END,
    max_payload_bytes    = CASE WHEN p_values ? 'max_payload_bytes'    THEN (p_values->>'max_payload_bytes')::int ELSE l.max_payload_bytes END,
    max_secrets          = CASE WHEN p_values ? 'max_secrets'          THEN (p_values->>'max_secrets')::int ELSE l.max_secrets END,
    max_connections      = CASE WHEN p_values ? 'max_connections'      THEN (p_values->>'max_connections')::int ELSE l.max_connections END
  WHERE l.tenant_id = p_tenant;
END
$$;
-- +goose StatementEnd
ALTER TABLE tenant_limits DROP COLUMN ai_monthly_tokens;
