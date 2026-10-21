-- Tell me (no-technical-steps N4, decision N0-4): a workflow's message to
-- the business's own people goes through the chat outbox like approval
-- requests, rendered when it is sent from the step's input in the run's
-- history, so the outbox still holds no text.

-- And events without a webhook: for a business that never set up a
-- provider's webhook in its dashboard, Taskiem asks the provider itself
-- (a connector trigger's poll), from where it last looked.

-- +goose Up
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice'));

-- Where polling a connection's trigger got to.
CREATE TABLE trigger_polls (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  connector     text NOT NULL,                     -- paystack@1
  trigger_name  text NOT NULL,
  connection    text NOT NULL,                     -- '' for the only one
  since         timestamptz NOT NULL,              -- the next poll asks from here
  polled_at     timestamptz,
  last_error    text,
  PRIMARY KEY (tenant_id, connector, trigger_name, connection)
);
ALTER TABLE trigger_polls ENABLE ROW LEVEL SECURITY;
ALTER TABLE trigger_polls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON trigger_polls TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON trigger_polls TO taskiem_app;

-- The dispatcher reads connector event triggers' names to poll them: what
-- is deployed where, never a definition or an option.
CREATE POLICY dispatch_poll ON triggers FOR SELECT TO taskiem_dispatch USING (type = 'connector_event');
GRANT SELECT (connector, trigger_name, connection, environment) ON triggers TO taskiem_dispatch;

-- The connector event triggers deployed in prod, one row per tenant,
-- connector, trigger and connection: what a poller may ask about.
-- +goose StatementBegin
CREATE FUNCTION taskiem_poll_targets()
RETURNS TABLE (tenant_id uuid, connector text, trigger_name text, connection text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT t.tenant_id, t.connector, t.trigger_name, COALESCE(t.connection, '') FROM triggers t
   WHERE t.type = 'connector_event' AND t.environment = 'prod'
   ORDER BY 1, 2, 3, 4
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_poll_targets() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_poll_targets() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_poll_targets() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_poll_targets();
REVOKE SELECT (connector, trigger_name, connection, environment) ON triggers FROM taskiem_dispatch;
DROP POLICY dispatch_poll ON triggers;
DROP TABLE trigger_polls;
DELETE FROM whatsapp_outbox WHERE kind = 'notice';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert'));
