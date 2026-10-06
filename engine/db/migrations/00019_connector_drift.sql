-- Contract drift (spec 6.4): where a connector's live output departed from
-- the output schema its manifest declares. One row per tenant, connector
-- version, action, path and kind; values are never stored, only types and
-- short non-personal enum values.

-- +goose Up
CREATE TABLE connector_drift (
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  connector       text NOT NULL, -- id@major
  version         text NOT NULL, -- exact
  action          text NOT NULL,
  path            text NOT NULL,
  kind            text NOT NULL CHECK (kind IN ('type', 'enum', 'missing')),
  expected        text NOT NULL,
  observed        text NOT NULL,
  first_seen      timestamptz NOT NULL DEFAULT now(),
  last_seen       timestamptz NOT NULL DEFAULT now(),
  occurrences     bigint NOT NULL DEFAULT 1,
  last_run_id     uuid,
  acknowledged_at timestamptz,
  acknowledged_by text,
  PRIMARY KEY (tenant_id, connector, version, action, path, kind)
);

ALTER TABLE connector_drift ENABLE ROW LEVEL SECURITY;
ALTER TABLE connector_drift FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON connector_drift TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON connector_drift TO taskiem_app;
GRANT UPDATE (observed, last_seen, occurrences, last_run_id, acknowledged_at, acknowledged_by) ON connector_drift TO taskiem_app;

-- +goose Down
DROP TABLE connector_drift;
