-- Roles, tenant scope helper. Spec 5.3, decision 0004.
-- Run migrations as the schema owner (a member of taskiem_dispatch, or a superuser).

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'taskiem_app') THEN
    CREATE ROLE taskiem_app NOLOGIN NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'taskiem_dispatch') THEN
    CREATE ROLE taskiem_dispatch NOLOGIN NOBYPASSRLS;
  END IF;
END
$$;
-- +goose StatementEnd

-- The caller's tenant scope, from SET LOCAL app.tenant_scope = '{uuid,...}'.
-- Unset or empty means no tenants: every RLS policy then matches nothing.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tenant_scope() RETURNS uuid[]
LANGUAGE sql STABLE PARALLEL SAFE AS $$
  SELECT COALESCE(NULLIF(current_setting('app.tenant_scope', true), '')::uuid[], '{}'::uuid[])
$$;
-- +goose StatementEnd

GRANT USAGE ON SCHEMA public TO taskiem_app, taskiem_dispatch;
GRANT EXECUTE ON FUNCTION taskiem_tenant_scope() TO taskiem_app, taskiem_dispatch;
