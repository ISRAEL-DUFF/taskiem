-- Daily usage snapshots (spec 16.3): each tenant's consumption against its
-- limits per UTC day, for invoices, the billing page and tuning prices
-- with data. Written by the billing job; a re-run of a day replaces it.
-- Partners see their sub-tenants' snapshots only in aggregate.

-- +goose Up
CREATE TABLE usage_snapshots (
  tenant_id           uuid NOT NULL REFERENCES tenants(id),
  day                 date NOT NULL,
  plan_id             text,
  runs_started        bigint NOT NULL DEFAULT 0,
  steps               bigint NOT NULL DEFAULT 0,  -- steps scheduled that day
  active_workflows    bigint NOT NULL DEFAULT 0,  -- workflows with a published version
  running_runs        bigint NOT NULL DEFAULT 0,  -- at snapshot time
  stored_runs         bigint NOT NULL DEFAULT 0,  -- runs kept (history not yet purged)
  whatsapp_templates  bigint NOT NULL DEFAULT 0,  -- month to date
  whatsapp_overage    bigint NOT NULL DEFAULT 0,  -- month to date
  ai_tokens           bigint NOT NULL DEFAULT 0,  -- month to date
  limits              jsonb NOT NULL DEFAULT '{}', -- effective limits that day
  taken_at            timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, day)
);
ALTER TABLE usage_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON usage_snapshots TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON usage_snapshots FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, UPDATE ON usage_snapshots TO taskiem_app;
GRANT SELECT ON usage_snapshots TO taskiem_dispatch;

-- A partner's sub-tenants' snapshots summed per day (counts only, no
-- sub-tenant data). The partner must be in scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_usage_snapshots(p_partner uuid, p_from date, p_to date)
RETURNS TABLE (day date, subtenants bigint, runs_started bigint, steps bigint, active_workflows bigint, running_runs bigint,
               stored_runs bigint, whatsapp_templates bigint, whatsapp_overage bigint, ai_tokens bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.day, count(*), sum(s.runs_started)::bigint, sum(s.steps)::bigint, sum(s.active_workflows)::bigint, sum(s.running_runs)::bigint,
         sum(s.stored_runs)::bigint, sum(s.whatsapp_templates)::bigint, sum(s.whatsapp_overage)::bigint, sum(s.ai_tokens)::bigint
    FROM usage_snapshots s JOIN tenants t ON t.id = s.tenant_id
   WHERE t.parent_id = p_partner AND p_partner = ANY (taskiem_tenant_scope())
     AND s.day BETWEEN p_from AND p_to
   GROUP BY s.day ORDER BY s.day
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_partner_usage_snapshots(uuid, date, date) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_partner_usage_snapshots(uuid, date, date) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_partner_usage_snapshots(uuid, date, date);
DROP TABLE usage_snapshots;
