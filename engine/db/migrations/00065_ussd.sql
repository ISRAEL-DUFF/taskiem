-- USSD fast path (spec 8.4, docs/ussd.md). A workflow with a ussd trigger
-- is routed by the service code its menu names; an aggregator's callbacks
-- reach the edge at /channels/ussd/<tenant>/<provider>, authenticated by
-- the tenant's channel token (and optionally an address allow-list); the
-- edge walks the menu inline and keeps each session here with a short
-- expiry, so every edge replica sees the same session. Only a confirmed
-- session reaches the engine, idempotently on its session id.

-- +goose Up
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd'));
ALTER TABLE triggers ADD COLUMN service_code text;
ALTER TABLE triggers ADD CONSTRAINT triggers_ussd_check CHECK ((type = 'ussd') = (service_code IS NOT NULL));
CREATE UNIQUE INDEX triggers_ussd_code ON triggers (tenant_id, environment, service_code) WHERE type = 'ussd';

-- A tenant's channel per aggregator: where its callbacks may come from.
-- The token is shown once; only its SHA-256 is kept.
CREATE TABLE ussd_channels (
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  provider       text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_]{1,31}$'),
  environment    text NOT NULL DEFAULT 'prod',
  token_hash     bytea NOT NULL CHECK (length(token_hash) = 32),
  allowed_cidrs  cidr[] NOT NULL DEFAULT '{}',
  status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  created_by     text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider)
);

-- One USSD session. The number is kept only as a hash salted with the
-- tenant (for rate limits; pseudonymous, not anonymous) until confirmation, when the caller's number and inputs are
-- sealed into data for the hand-off and the SMS; data is dropped once the
-- outcome is sent. Rows are purged a day after they expire.
CREATE TABLE ussd_sessions (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  provider      text NOT NULL,
  session_id    text NOT NULL CHECK (length(session_id) BETWEEN 1 AND 128),
  number_hash   bytea NOT NULL,
  network       text NOT NULL DEFAULT '',
  environment   text NOT NULL,
  service_code  text NOT NULL,
  workflow_id   uuid NOT NULL,
  version       int NOT NULL,
  reference     text NOT NULL,
  state         text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'ended', 'confirmed', 'started', 'refused')),
  path          jsonb,               -- sealed inputs so far, for incremental aggregators only
  data          jsonb,               -- sealed caller number and inputs, from confirmation to the outcome
  notify        boolean NOT NULL DEFAULT false,
  run_id        uuid,
  reason        text,                -- why a confirmed session was refused (no personal data)
  notified_at   timestamptz,
  notice        text CHECK (notice IN ('sent', 'failed', 'skipped')),
  confirmed_at  timestamptz,
  expires_at    timestamptz NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider, session_id)
);
CREATE INDEX ussd_sessions_number ON ussd_sessions (tenant_id, number_hash, created_at);
CREATE INDEX ussd_sessions_pending ON ussd_sessions (tenant_id, confirmed_at)
  WHERE state = 'confirmed' OR (notify AND notified_at IS NULL AND state IN ('started', 'refused'));

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['ussd_channels', 'ussd_sessions'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- The notifier finds tenants with USSD work (hand-offs, outcomes, purges)
-- without reading anything else of theirs.
CREATE POLICY dispatch ON ussd_sessions FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id) ON ussd_sessions TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_ussd_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT s.tenant_id FROM ussd_sessions s JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ussd_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ussd_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ussd_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_ussd_tenants();
DROP TABLE ussd_sessions;
DROP TABLE ussd_channels;
DELETE FROM triggers WHERE type = 'ussd';
DROP INDEX triggers_ussd_code;
ALTER TABLE triggers DROP CONSTRAINT triggers_ussd_check;
ALTER TABLE triggers DROP COLUMN service_code;
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event'));
