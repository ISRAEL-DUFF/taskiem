-- WhatsApp template cost accounting (spec 16): Meta charges per template
-- message by category (marketing, utility, authentication). Taskiem counts
-- the templates it sends for each tenant per UTC month and category against
-- the plan's allowance (whatsapp_templates_monthly, included in the plan),
-- and counts those beyond it for pass-through billing. Approvals, codes and
-- alerts are never held back by the allowance; only marketing templates
-- are (docs/whatsapp.md#template-costs).
--
-- taskiem_set_tenant_limits now takes any column of tenant_limits, so a new
-- limit needs only its column.

-- +goose Up
ALTER TABLE tenant_limits ADD COLUMN whatsapp_templates_monthly bigint CHECK (whatsapp_templates_monthly >= 0);

CREATE TABLE whatsapp_template_usage (
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  month           date NOT NULL CHECK (month = date_trunc('month', month)::date),
  category        text NOT NULL CHECK (category IN ('authentication', 'utility', 'marketing')),
  sent            bigint NOT NULL DEFAULT 0,
  over_allowance  bigint NOT NULL DEFAULT 0, -- sent beyond the allowance: billed through
  blocked         bigint NOT NULL DEFAULT 0, -- not sent: beyond the allowance and blockable
  updated_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, month, category)
);
ALTER TABLE whatsapp_template_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_template_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON whatsapp_template_usage TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON whatsapp_template_usage TO taskiem_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_set_tenant_limits(p_tenant uuid, p_values jsonb, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  k text;
  known text[];
  sets text;
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  SELECT array_agg(a.attname::text) INTO known FROM pg_attribute a
   WHERE a.attrelid = 'public.tenant_limits'::regclass AND a.attnum > 0 AND NOT a.attisdropped
     AND a.attname NOT IN ('tenant_id', 'updated_by', 'updated_at');
  FOR k IN SELECT jsonb_object_keys(p_values) LOOP
    IF NOT (k = ANY (known)) THEN
      RAISE EXCEPTION 'unknown limit %', k USING ERRCODE = '22023';
    END IF;
  END LOOP;
  INSERT INTO tenant_limits (tenant_id, updated_by) VALUES (p_tenant, p_by)
    ON CONFLICT (tenant_id) DO UPDATE SET updated_by = EXCLUDED.updated_by, updated_at = now();
  -- Each named limit takes its value (null: back to the platform default),
  -- cast to its column's type; the others stay.
  SELECT string_agg(format('%I = CASE WHEN $2 ? %L THEN v.%I ELSE l.%I END', c, c, c, c), ', ') INTO sets FROM unnest(known) c;
  EXECUTE format('UPDATE tenant_limits l SET %s FROM jsonb_populate_record(NULL::public.tenant_limits, $2) v WHERE l.tenant_id = $1', sets)
    USING p_tenant, p_values;
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
DROP TABLE whatsapp_template_usage;
ALTER TABLE tenant_limits DROP COLUMN whatsapp_templates_monthly;
