-- Data tables (decision 0033; docs/data-tables.md).
--
-- A data table is a Postgres table in a Xenibase project, plus Taskiem's
-- record of it here: its name, title, columns (a small list of types, a
-- required flag, a personal-data flag) and nothing else. The rows live
-- only in Xenibase and are read and written through the xenibase@1
-- connection named data_tables in prod.
--
-- data_table_projects is the organisation's Xenibase project: the one
-- Taskiem provisions in the platform's organisation on the first table
-- (own = false; Taskiem renews its token), or one the organisation
-- brought itself (own = true). Provisioning takes minutes and several
-- calls, so its state is recorded step by step (status) and a request
-- that finds a half-made project resumes it rather than making another;
-- lease_until keeps two requests from provisioning at once.
--
-- max_data_tables and max_data_table_columns are the plan limits
-- (docs/operations.md#plan-limits).

-- +goose Up
ALTER TABLE tenant_limits
  ADD COLUMN max_data_tables int CHECK (max_data_tables >= 0),
  ADD COLUMN max_data_table_columns int CHECK (max_data_table_columns >= 0);

CREATE TABLE data_table_projects (
  tenant_id        uuid PRIMARY KEY REFERENCES tenants(id),
  status           text NOT NULL CHECK (status IN ('creating', 'enabling', 'ready', 'failed')),
  own              boolean NOT NULL DEFAULT false,       -- the organisation's own project (Settings > Data tables)
  project_name     text NOT NULL,                         -- taskiem-<tenant id>, or the own project's name
  project_id       text,                                  -- Xenibase's project id, once known
  operation_id     text,                                  -- the Xenibase operation being waited for
  ref              text,                                  -- the project reference in its data API hostname
  api_url          text,
  connection_id    uuid REFERENCES connections(id),       -- the data_tables connection
  token_expires_at timestamptz,                           -- the connection's token; renewed two weeks before
  lease_until      timestamptz,                           -- held by the request provisioning now
  error            text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE data_table_projects ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_table_projects FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON data_table_projects TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON data_table_projects TO taskiem_app;
-- The renewal job lists due projects before any tenant scope exists
-- (taskiem_data_table_renewals below).
CREATE POLICY dispatch ON data_table_projects FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, own, status, token_expires_at) ON data_table_projects TO taskiem_dispatch;

CREATE TABLE data_tables (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  name         text NOT NULL CHECK (name ~ '^[a-z][a-z0-9_]{0,62}$'),  -- the Postgres table, public.<name>
  title        text NOT NULL DEFAULT '',
  description  text NOT NULL DEFAULT '',
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name)
);
ALTER TABLE data_tables ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_tables FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON data_tables TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON data_tables TO taskiem_app;

CREATE TABLE data_table_columns (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  table_id     uuid NOT NULL REFERENCES data_tables(id) ON DELETE CASCADE,
  name         text NOT NULL CHECK (name ~ '^[a-z][a-z0-9_]{0,62}$'),
  title        text NOT NULL DEFAULT '',
  type         text NOT NULL CHECK (type IN ('text', 'integer', 'number', 'boolean', 'date', 'datetime', 'phone', 'email', 'json')),
  required     boolean NOT NULL DEFAULT false,
  personal     boolean NOT NULL DEFAULT false,            -- masked in logs, audit and the builder's context
  position     int NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (table_id, name)
);
ALTER TABLE data_table_columns ENABLE ROW LEVEL SECURITY;
ALTER TABLE data_table_columns FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON data_table_columns TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON data_table_columns TO taskiem_app;

-- Tenants whose platform-provisioned project's token expires before
-- p_before, for the renewal job, before any tenant scope exists.
-- +goose StatementBegin
CREATE FUNCTION taskiem_data_table_renewals(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.tenant_id FROM data_table_projects p
   WHERE NOT p.own AND p.status = 'ready' AND p.token_expires_at IS NOT NULL AND p.token_expires_at < p_before
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_data_table_renewals(timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_data_table_renewals(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_data_table_renewals(timestamptz) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_data_table_renewals(timestamptz);
DROP TABLE data_table_columns;
DROP TABLE data_tables;
DROP TABLE data_table_projects;
ALTER TABLE tenant_limits DROP COLUMN max_data_tables, DROP COLUMN max_data_table_columns;
