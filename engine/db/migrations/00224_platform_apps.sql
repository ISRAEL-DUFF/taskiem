-- Platform apps (decision 0051; connector/v1 rule 18).
--
-- Slack, Meta and Intuit send every business's events to the one webhook
-- URL of the app Taskiem registered with them, naming the account each is
-- for (a Slack team, a Facebook Page, a QuickBooks company). A connection
-- made through Taskiem's app records that account here when it is made;
-- the edge verifies a delivery with the app's secret and asks
-- taskiem_app_route_event which connections hold the account, in whichever
-- tenants, then delivers into each as if it had come to that tenant's own
-- URL. That function is the one step that crosses tenants: it reads this
-- table only, which holds routing columns and never a credential, and
-- returns no more than where to deliver.
--
-- An event for an account no connection holds is acknowledged (providers
-- retry what is not, for days after an uninstall) and counted per day in
-- app_event_drops, without its body.

-- +goose Up
CREATE TABLE connection_app_accounts (
  connection_id uuid PRIMARY KEY REFERENCES connections(id) ON DELETE CASCADE,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  environment   text NOT NULL,
  connector     text NOT NULL,                    -- the manifest id, slack
  connection    text NOT NULL,                    -- the connection's name
  client        text NOT NULL,                    -- the platform app, slack (zoho for every Zoho connector)
  account_key   text NOT NULL CHECK (length(account_key) BETWEEN 1 AND 255)
);
CREATE INDEX connection_app_accounts_lookup ON connection_app_accounts (connector, account_key);
ALTER TABLE connection_app_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE connection_app_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON connection_app_accounts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON connection_app_accounts TO taskiem_app;
CREATE POLICY dispatch ON connection_app_accounts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON connection_app_accounts TO taskiem_dispatch;
-- The definer reads which connections are active: their id and status
-- (00200 granted the others), never their credentials.
CREATE POLICY app_routing ON connections FOR SELECT TO taskiem_dispatch USING (status = 'active');
GRANT SELECT (id) ON connections TO taskiem_dispatch;

CREATE TABLE app_event_drops (
  day          date NOT NULL,
  connector    text NOT NULL,
  trigger_name text NOT NULL,
  hits         bigint NOT NULL DEFAULT 1,
  last_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (day, connector, trigger_name)
);
ALTER TABLE app_event_drops ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_event_drops FORCE ROW LEVEL SECURITY;
CREATE POLICY dispatch ON app_event_drops TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE ON app_event_drops TO taskiem_dispatch;

-- The active connections, in tenants that are not deleted, holding a
-- provider account for a connector. Suspended tenants are returned: the
-- edge then answers 423 so the provider retries, as at the tenant's own
-- URL.
-- +goose StatementBegin
CREATE FUNCTION taskiem_app_route_event(p_connector text, p_account text)
RETURNS TABLE (tenant_id uuid, environment text, connection text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.tenant_id, a.environment, a.connection
    FROM connection_app_accounts a
    JOIN connections c ON c.id = a.connection_id AND c.status = 'active'
    JOIN tenants t ON t.id = a.tenant_id AND t.status <> 'deleted'
   WHERE a.connector = p_connector AND a.account_key = p_account AND p_account <> ''
   ORDER BY a.tenant_id, a.environment, a.connection;
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_app_route_event(text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_app_route_event(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_app_route_event(text, text) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_app_event_dropped(p_connector text, p_trigger text)
RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO app_event_drops (day, connector, trigger_name) VALUES ((now() AT TIME ZONE 'UTC')::date, p_connector, p_trigger)
  ON CONFLICT (day, connector, trigger_name) DO UPDATE SET hits = app_event_drops.hits + 1, last_at = now();
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_app_event_dropped(text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_app_event_dropped(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_app_event_dropped(text, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_app_event_dropped(text, text);
DROP FUNCTION taskiem_app_route_event(text, text);
DROP TABLE app_event_drops;
REVOKE SELECT (id) ON connections FROM taskiem_dispatch;
DROP POLICY app_routing ON connections;
DROP TABLE connection_app_accounts;
