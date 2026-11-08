-- The row-expiry sweep reads the tables due a page at a time, after the
-- last (tenant, table) it saw, so it reaches every one however many
-- businesses keep rows for a time (00317 read the first thousand only).

-- +goose Up
DROP FUNCTION taskiem_tables_to_expire(int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_tables_to_expire(p_after_tenant uuid, p_after_table uuid, p_limit int)
RETURNS TABLE (tenant_id uuid, table_id uuid, name text, keep_days int, conversations boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.tenant_id, t.id, t.name,
         CASE WHEN t.keep_with_conversations THEN COALESCE(cs.conversation_days, 30) ELSE t.keep_days END,
         t.keep_with_conversations
    FROM data_tables t
    LEFT JOIN tenant_channel_settings cs ON cs.tenant_id = t.tenant_id
   WHERE t.kept_by IS NULL AND (t.keep_days IS NOT NULL OR t.keep_with_conversations)
     AND (p_after_tenant IS NULL OR (t.tenant_id, t.id) > (p_after_tenant, p_after_table))
   ORDER BY t.tenant_id, t.id
   LIMIT p_limit
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_tables_to_expire(uuid, uuid, int) OWNER TO taskiem_dispatch;
REVOKE ALL ON FUNCTION taskiem_tables_to_expire(uuid, uuid, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_tables_to_expire(uuid, uuid, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_tables_to_expire(uuid, uuid, int);
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
