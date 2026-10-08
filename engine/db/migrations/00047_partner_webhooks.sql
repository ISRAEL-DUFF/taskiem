-- Partner webhooks (spec 13.4 step 5; decision 0015): events about a
-- partner's sub-tenants, queued in the partner's own tenant and delivered
-- signed (like alert webhooks) with retries; the rows are the delivery log.
-- Run and publish events are queued by triggers, in the transaction that
-- ends the run or publishes the version, so none is lost or invented.

-- +goose Up
CREATE TABLE partner_webhook_deliveries (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),     -- the partner
  app_id          uuid NOT NULL REFERENCES embed_apps(id),
  event           text NOT NULL CHECK (event IN ('run.completed', 'run.failed', 'workflow.published', 'usage.threshold')),
  sub_tenant_id   uuid REFERENCES tenants(id),              -- NULL for partner-wide usage events
  dedup_key       text NOT NULL,
  payload         jsonb NOT NULL,
  status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'failed')),
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  last_status     int,                                      -- the endpoint's HTTP status
  last_error      text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  delivered_at    timestamptz,
  UNIQUE (app_id, dedup_key)
);
CREATE INDEX partner_webhook_deliveries_due ON partner_webhook_deliveries (tenant_id, next_attempt_at) WHERE status = 'pending';
CREATE INDEX partner_webhook_deliveries_log ON partner_webhook_deliveries (tenant_id, created_at);
ALTER TABLE partner_webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE partner_webhook_deliveries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON partner_webhook_deliveries TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch_queue ON partner_webhook_deliveries FOR INSERT TO taskiem_dispatch WITH CHECK (true);
-- ON CONFLICT reads the arbiter's columns.
CREATE POLICY dispatch_dedup ON partner_webhook_deliveries FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, UPDATE ON partner_webhook_deliveries TO taskiem_app;
GRANT INSERT, SELECT (app_id, dedup_key) ON partner_webhook_deliveries TO taskiem_dispatch;

-- Queue an event of a sub-tenant for each of its partner's active apps
-- that has a webhook and subscribes to the event.
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_queue(p_sub uuid, p_event text, p_dedup text, p_payload jsonb)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO partner_webhook_deliveries (id, tenant_id, app_id, event, sub_tenant_id, dedup_key, payload)
  SELECT gen_random_uuid(), a.tenant_id, a.id, p_event, p_sub, p_dedup, p_payload
    FROM tenants s
    JOIN embed_apps a ON a.tenant_id = s.parent_id AND a.status = 'active' AND a.webhook_url IS NOT NULL AND p_event = ANY (a.webhook_events)
   WHERE s.id = p_sub AND s.parent_id IS NOT NULL
  ON CONFLICT (app_id, dedup_key) DO NOTHING
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_partner_queue(uuid, text, text, jsonb) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_partner_queue(uuid, text, text, jsonb) FROM PUBLIC;

-- Run outcomes: status and timing only, never inputs or outputs.
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_run_ended() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_event text := 'run.' || NEW.status;
BEGIN
  PERFORM taskiem_partner_queue(NEW.tenant_id, v_event, v_event || ':' || NEW.id::text, jsonb_build_object(
    'run', jsonb_build_object('id', NEW.id, 'workflow_id', NEW.workflow_id, 'version', NEW.version, 'environment', NEW.environment,
                              'status', NEW.status, 'started_at', NEW.started_at, 'ended_at', NEW.ended_at)));
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_partner_run_ended() OWNER TO taskiem_dispatch;
CREATE TRIGGER runs_partner_webhook AFTER UPDATE OF status ON runs FOR EACH ROW
  WHEN (NEW.status IN ('completed', 'failed') AND OLD.status IS DISTINCT FROM NEW.status)
  EXECUTE FUNCTION taskiem_partner_run_ended();

-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_version_published() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.state <> 'published' OR (TG_OP = 'UPDATE' AND OLD.state = 'published') THEN
    RETURN NULL;
  END IF;
  PERFORM taskiem_partner_queue(NEW.tenant_id, 'workflow.published', 'workflow.published:' || NEW.workflow_id::text || '/' || NEW.version,
    jsonb_build_object('workflow', jsonb_build_object('id', NEW.workflow_id, 'version', NEW.version, 'published_at', COALESCE(NEW.published_at, now()))));
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_partner_version_published() OWNER TO taskiem_dispatch;
CREATE TRIGGER workflow_versions_partner_webhook AFTER INSERT OR UPDATE OF state ON workflow_versions FOR EACH ROW
  EXECUTE FUNCTION taskiem_partner_version_published();

-- The scheduler's webhook loop: active partners (routing columns only).
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.tenant_id FROM partners p JOIN tenants t ON t.id = p.tenant_id AND t.status = 'active' ORDER BY p.tenant_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_partner_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_partner_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_partner_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_partner_tenants();
DROP TRIGGER workflow_versions_partner_webhook ON workflow_versions;
DROP FUNCTION taskiem_partner_version_published();
DROP TRIGGER runs_partner_webhook ON runs;
DROP FUNCTION taskiem_partner_run_ended();
DROP FUNCTION taskiem_partner_queue(uuid, text, text, jsonb);
DROP TABLE partner_webhook_deliveries;
