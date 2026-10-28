-- Taskiem's own provider clients (decision 0052; iswallet account numbers,
-- workstream W1). Taskiem holds an iswallet client of its own and gives a
-- business a wallet under it: the business's connection iswallet/taskiem
-- stores no key. The vault hands its connector the platform's key for the
-- connection's mode (process configuration, TASKIEM_ISWALLET_*), with the
-- allow-list of the one wallet the connection is for, and refuses to
-- replace its credentials: only Taskiem writes them.
--
-- The platform's one subscription per mode delivers every business's
-- events to /hooks/platform/{connector}; taskiem_platform_route_event
-- says which connections hold the wallet an event names, as
-- taskiem_app_route_event does for platform apps (00228), but only among
-- connections Taskiem made under its own client, in the same mode, so a
-- business that lists another's wallet on a connection of its own hears
-- nothing of it.

-- +goose Up
ALTER TABLE connections ADD COLUMN platform text CHECK (platform IS NULL OR platform = 'taskiem');
GRANT SELECT (platform, mode) ON connections TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_platform_route_event(p_connector text, p_mode text, p_account text)
RETURNS TABLE (tenant_id uuid, environment text, connection text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.tenant_id, a.environment, a.connection
    FROM connection_app_accounts a
    JOIN connections c ON c.id = a.connection_id AND c.status = 'active' AND c.platform = 'taskiem' AND c.mode = p_mode
    JOIN tenants t ON t.id = a.tenant_id AND t.status <> 'deleted'
   WHERE a.connector = p_connector AND a.client = 'taskiem' AND a.account_key = p_account AND p_account <> ''
   ORDER BY a.tenant_id, a.environment, a.connection;
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_platform_route_event(text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_platform_route_event(text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_platform_route_event(text, text, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_platform_route_event(text, text, text);
REVOKE SELECT (platform, mode) ON connections FROM taskiem_dispatch;
ALTER TABLE connections DROP COLUMN platform;
