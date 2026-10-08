-- Remote trigger registration (decision 0021, connector/v1
-- registration: remote). When a workflow whose trigger is registered
-- remotely is deployed to an environment, the deploying transaction
-- records here what the provider should hold (desired = 'present'); when
-- it leaves the environment, that the subscription should go
-- (desired = 'absent'). The intent is recorded before any call to the
-- provider; the reconciler (engine/remote) then calls the provider and
-- records what it answered, retrying until the two agree, and deletes the
-- row only once the provider's subscription is gone. A subscription's
-- signing secret is kept in the vault (environment _remote), never here.
--
-- triggers.options: a connector trigger's options (wd/v1
-- trigger.config.options), read at ingest by the connector's enricher.
--
-- taskiem_remote_tenants: the reconciler's cross-tenant routing (tenant
-- ids only); the work itself runs in each tenant's scope.

-- +goose Up
ALTER TABLE triggers ADD COLUMN options jsonb;

CREATE TABLE remote_subscriptions (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  workflow_id     uuid NOT NULL REFERENCES workflows(id),
  environment     text NOT NULL,
  connector       text NOT NULL,          -- "pgdock@1"
  trigger_name    text NOT NULL,          -- manifest trigger, e.g. row_changed
  connection      text,                   -- the connection's name; NULL: the only active one
  version         int  NOT NULL,          -- the deployed version it serves
  events          text[] NOT NULL DEFAULT '{}',
  options         jsonb NOT NULL DEFAULT '{}',
  desired         text NOT NULL CHECK (desired IN ('present', 'absent')),
  -- What the provider holds: its id, and the hash of the spec (events,
  -- options, ingest URL) last applied; NULL applied_hash means apply (and
  -- repair) again.
  remote_id       text,
  applied_hash    text,
  state           text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'active', 'failed')),
  remote_status   text CHECK (remote_status IN ('healthy', 'failing', 'paused', 'broken', 'missing')),
  status_reason   text,
  last_error      text,
  attempts        int NOT NULL DEFAULT 0,
  next_attempt_at timestamptz,            -- NULL: nothing to do before the next health check
  checked_at      timestamptz,
  last_test_at    timestamptz,            -- the provider's last test event
  lease_until     timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
-- One live subscription per workflow and environment (a WD has one trigger).
CREATE UNIQUE INDEX remote_subscriptions_live ON remote_subscriptions (workflow_id, environment) WHERE desired = 'present';
CREATE INDEX remote_subscriptions_due ON remote_subscriptions (next_attempt_at) WHERE next_attempt_at IS NOT NULL;
CREATE INDEX remote_subscriptions_tenant ON remote_subscriptions (tenant_id, environment, connector);
ALTER TABLE remote_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE remote_subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON remote_subscriptions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON remote_subscriptions TO taskiem_app;
-- The reconciler finds tenants with work due without entering each.
CREATE POLICY dispatch ON remote_subscriptions FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, desired, next_attempt_at, checked_at) ON remote_subscriptions TO taskiem_dispatch;

-- Tenants with a subscription to apply, remove, retry, or health-check.
-- +goose StatementBegin
CREATE FUNCTION taskiem_remote_tenants(p_check_before timestamptz, p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM remote_subscriptions s
    JOIN tenants t ON t.id = s.tenant_id AND t.status <> 'deleted'
   WHERE (s.next_attempt_at IS NOT NULL AND s.next_attempt_at <= now())
      OR (s.desired = 'present' AND s.remote_id IS NOT NULL AND (s.checked_at IS NULL OR s.checked_at < p_check_before))
   GROUP BY s.tenant_id
   ORDER BY min(COALESCE(s.next_attempt_at, s.checked_at, s.created_at))
   LIMIT p_limit
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_remote_tenants(timestamptz, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_remote_tenants(timestamptz, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_remote_tenants(timestamptz, int);
DROP TABLE remote_subscriptions;
ALTER TABLE triggers DROP COLUMN options;
