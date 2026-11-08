-- Conversations kept for a time (decision 0062 V5, M3 and M6, Q4): a data
-- table may keep its rows for so many days (keep_days), or for as long as
-- the business keeps its conversations with customers
-- (keep_with_conversations, following tenant_channel_settings.
-- conversation_days, 30 by default and capped by the plan). The api role's
-- sweep deletes older rows through the table's connection; runs whose
-- workflow keeps them "conversations" long, and their files, end with them.

-- +goose Up
ALTER TABLE data_tables
  ADD COLUMN keep_days int CHECK (keep_days BETWEEN 1 AND 3650),
  ADD COLUMN keep_with_conversations boolean NOT NULL DEFAULT false;
ALTER TABLE tenant_channel_settings
  ADD COLUMN conversation_days int NOT NULL DEFAULT 30 CHECK (conversation_days BETWEEN 1 AND 3650);

GRANT INSERT (keep_days, keep_with_conversations), UPDATE (keep_days, keep_with_conversations) ON data_tables TO taskiem_app;

-- The sweep reads, across tenants, only the tables that keep rows for a
-- time and only the businesses' conversation days.
CREATE POLICY expire_read ON data_tables FOR SELECT TO taskiem_dispatch USING (keep_days IS NOT NULL OR keep_with_conversations);
CREATE POLICY expire_read ON tenant_channel_settings FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, conversation_days) ON tenant_channel_settings TO taskiem_dispatch;

-- The tables whose old rows are due to go, across tenants, for the sweep:
-- ids, names and days only, never rows. A kept table is never swept.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tables_to_expire(p_limit int) RETURNS TABLE (tenant_id uuid, table_id uuid, name text, keep_days int, conversations boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.tenant_id, t.id, t.name,
         CASE WHEN t.keep_with_conversations THEN COALESCE(cs.conversation_days, 30) ELSE t.keep_days END,
         t.keep_with_conversations
    FROM data_tables t
    LEFT JOIN tenant_channel_settings cs ON cs.tenant_id = t.tenant_id
   WHERE t.kept_by IS NULL AND (t.keep_days IS NOT NULL OR t.keep_with_conversations)
   ORDER BY t.tenant_id, t.id
   LIMIT p_limit
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_tables_to_expire(int) OWNER TO taskiem_dispatch;
REVOKE ALL ON FUNCTION taskiem_tables_to_expire(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_tables_to_expire(int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_tables_to_expire(int);
REVOKE SELECT (tenant_id, conversation_days) ON tenant_channel_settings FROM taskiem_dispatch;
DROP POLICY expire_read ON tenant_channel_settings;
DROP POLICY expire_read ON data_tables;
ALTER TABLE tenant_channel_settings DROP COLUMN conversation_days;
ALTER TABLE data_tables DROP COLUMN keep_with_conversations, DROP COLUMN keep_days;
