-- Kept tables (decision 0045 point 6; docs/spec/invoices-and-money-log.md
-- 7.1; docs/data-tables.md#kept-tables).
--
-- A data table may be kept by Taskiem: its rows are written only by
-- Taskiem's own writers (the money log, matching), and people and
-- workflows may read, search and export it but not change it. kept_by
-- names the feature that keeps it ('money_log'); NULL is an ordinary
-- table.
--
-- The rule this schema keeps: the app role can neither set nor clear
-- kept_by (it holds no INSERT or UPDATE privilege on the column), and
-- cannot change or delete a kept table's record or its columns (the
-- guard triggers). A kept table is recorded, and its columns added, only
-- through taskiem_data_table_keep and taskiem_data_table_keep_column,
-- which run as taskiem_dispatch, refuse to take over an ordinary table
-- and never clear kept_by. Nothing clears it: a later change that lets a
-- feature give its table back adds that function.
--
-- The rows themselves live in Xenibase, where Postgres here cannot guard
-- them: the API, chat and the workflow worker refuse writes to a kept
-- table before they reach Xenibase (engine/datatables.ErrKept).
-- +goose Up
ALTER TABLE data_tables ADD COLUMN kept_by text CHECK (kept_by IS NULL OR kept_by ~ '^[a-z][a-z0-9_]{0,62}$');

-- kept_by is left out of the app role's column privileges.
REVOKE INSERT, UPDATE ON data_tables FROM taskiem_app;
GRANT INSERT (id, tenant_id, name, title, description, created_by, created_at, updated_at) ON data_tables TO taskiem_app;
GRANT UPDATE (title, description, updated_at) ON data_tables TO taskiem_app;

-- The keeping functions run as taskiem_dispatch, within the caller's
-- tenant scope.
CREATE POLICY kept_writer ON data_tables TO taskiem_dispatch
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON data_tables TO taskiem_dispatch;
GRANT UPDATE (updated_at) ON data_tables TO taskiem_dispatch;
CREATE POLICY kept_writer ON data_table_columns TO taskiem_dispatch
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON data_table_columns TO taskiem_dispatch;

-- A kept table's record and columns change only through the keeping
-- functions (or by a superuser, by hand).
-- +goose StatementBegin
CREATE FUNCTION taskiem_data_table_kept_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  kept text;
  tname text;
BEGIN
  IF current_user = 'taskiem_dispatch' OR EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user AND rolsuper) THEN
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
  END IF;
  IF TG_TABLE_NAME = 'data_tables' THEN
    IF TG_OP = 'INSERT' THEN
      kept := NEW.kept_by;
      tname := NEW.name;
    ELSE
      kept := OLD.kept_by;
      tname := OLD.name;
      IF kept IS NULL AND TG_OP = 'UPDATE' THEN
        kept := NEW.kept_by;
      END IF;
    END IF;
  ELSE
    SELECT t.kept_by, t.name INTO kept, tname FROM data_tables t
     WHERE t.id = CASE WHEN TG_OP = 'DELETE' THEN OLD.table_id ELSE NEW.table_id END;
  END IF;
  IF kept IS NOT NULL THEN
    RAISE EXCEPTION 'the data table % is kept by Taskiem (%): it cannot be changed or deleted', tname, kept USING ERRCODE = '42501';
  END IF;
  IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER data_tables_kept_guard BEFORE INSERT OR UPDATE OR DELETE ON data_tables
  FOR EACH ROW EXECUTE FUNCTION taskiem_data_table_kept_guard();
CREATE TRIGGER data_table_columns_kept_guard BEFORE INSERT OR UPDATE OR DELETE ON data_table_columns
  FOR EACH ROW EXECUTE FUNCTION taskiem_data_table_kept_guard();

-- Records a kept table of the tenant in scope, without columns, unless a
-- table of that name exists; either way returns the table of that name.
-- An ordinary table comes back with kept_by NULL and is left as it is:
-- the caller refuses to take it over.
-- +goose StatementBegin
CREATE FUNCTION taskiem_data_table_keep(p_tenant uuid, p_id uuid, p_name text, p_title text, p_description text, p_kept_by text)
RETURNS TABLE (id uuid, kept_by text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
#variable_conflict use_column
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'the tenant is not in scope' USING ERRCODE = '42501';
  END IF;
  IF p_kept_by IS NULL OR p_kept_by !~ '^[a-z][a-z0-9_]{0,62}$' THEN
    RAISE EXCEPTION 'kept_by must name the feature keeping the table' USING ERRCODE = '22023';
  END IF;
  INSERT INTO data_tables (id, tenant_id, name, title, description, created_by, kept_by)
  VALUES (p_id, p_tenant, p_name, COALESCE(p_title, ''), COALESCE(p_description, ''), 'taskiem:' || p_kept_by, p_kept_by)
  ON CONFLICT (tenant_id, name) DO NOTHING;
  RETURN QUERY SELECT t.id, t.kept_by FROM data_tables t WHERE t.tenant_id = p_tenant AND t.name = p_name;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_data_table_keep(uuid, uuid, text, text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_data_table_keep(uuid, uuid, text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_data_table_keep(uuid, uuid, text, text, text, text) TO taskiem_app;

-- Records a column of a kept table after its others, once: a column
-- already recorded is left as it is. Refused for an ordinary table.
-- +goose StatementBegin
CREATE FUNCTION taskiem_data_table_keep_column(p_tenant uuid, p_table uuid, p_name text, p_title text, p_type text, p_required boolean, p_personal boolean)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  added int;
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'the tenant is not in scope' USING ERRCODE = '42501';
  END IF;
  PERFORM 1 FROM data_tables t WHERE t.id = p_table AND t.tenant_id = p_tenant AND t.kept_by IS NOT NULL;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'the data table is not kept' USING ERRCODE = '42501';
  END IF;
  INSERT INTO data_table_columns (tenant_id, table_id, name, title, type, required, personal, position)
  SELECT p_tenant, p_table, p_name, COALESCE(p_title, ''), p_type, p_required, p_personal, COALESCE(max(c.position), -1) + 1
    FROM data_table_columns c WHERE c.table_id = p_table
  ON CONFLICT (table_id, name) DO NOTHING;
  GET DIAGNOSTICS added = ROW_COUNT;
  IF added > 0 THEN
    UPDATE data_tables SET updated_at = now() WHERE data_tables.id = p_table;
  END IF;
  RETURN added > 0;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_data_table_keep_column(uuid, uuid, text, text, text, boolean, boolean) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_data_table_keep_column(uuid, uuid, text, text, text, boolean, boolean) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_data_table_keep_column(uuid, uuid, text, text, text, boolean, boolean) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_data_table_keep_column(uuid, uuid, text, text, text, boolean, boolean);
DROP FUNCTION taskiem_data_table_keep(uuid, uuid, text, text, text, text);
DROP TRIGGER data_table_columns_kept_guard ON data_table_columns;
DROP TRIGGER data_tables_kept_guard ON data_tables;
DROP FUNCTION taskiem_data_table_kept_guard();
REVOKE ALL ON data_table_columns FROM taskiem_dispatch;
DROP POLICY kept_writer ON data_table_columns;
REVOKE ALL ON data_tables FROM taskiem_dispatch;
DROP POLICY kept_writer ON data_tables;
REVOKE INSERT, UPDATE ON data_tables FROM taskiem_app;
GRANT INSERT, UPDATE ON data_tables TO taskiem_app;
ALTER TABLE data_tables DROP COLUMN kept_by;
