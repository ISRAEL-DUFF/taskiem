-- A run started in the web's Taskiem conversation (or a tool started on
-- the web asking its answer there) is watched with channel web; the chat
-- tick must visit its business even when no member linked a chat app, or
-- its answer never reaches the conversation (made tools M2/M4).
-- +goose Up
-- The dispatch role reads which businesses have a web watch waiting, and
-- nothing else of them.
CREATE POLICY dispatch_web_watches ON whatsapp_run_watches TO taskiem_dispatch USING (channel = 'web' AND notified_at IS NULL);
GRANT SELECT (tenant_id, channel, notified_at) ON whatsapp_run_watches TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_web_watch_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT w.tenant_id FROM whatsapp_run_watches w
    JOIN tenants t ON t.id = w.tenant_id AND t.status = 'active'
   WHERE w.notified_at IS NULL AND w.channel = 'web'
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_web_watch_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_web_watch_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_web_watch_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_web_watch_tenants();
REVOKE SELECT (tenant_id, channel, notified_at) ON whatsapp_run_watches FROM taskiem_dispatch;
DROP POLICY dispatch_web_watches ON whatsapp_run_watches;
