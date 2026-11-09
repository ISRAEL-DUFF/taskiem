-- WhatsApp numbers' quality (decision 0062 V10, C23): Meta rates how a
-- business number's customers receive its messages (GREEN, YELLOW, RED),
-- and lowers the number's messaging limit, or restricts it, when they
-- block or report it. A business whose assistant annoys people would not
-- know: Taskiem reads each assistant's number every few hours, keeps what
-- it read last, and tells the owners when it gets worse.
--
-- whatsapp_number_health   one row per WhatsApp connection an assistant
--                          answers on: the rating, the limit tier and
--                          the status last read. No personal data.

-- +goose Up
CREATE TABLE whatsapp_number_health (
  tenant_id            uuid NOT NULL REFERENCES tenants(id),
  environment          text NOT NULL,
  connection           text NOT NULL,
  quality_rating       text NOT NULL DEFAULT '',
  messaging_limit_tier text NOT NULL DEFAULT '',
  status               text NOT NULL DEFAULT '',
  checked_at           timestamptz NOT NULL DEFAULT now(),
  changed_at           timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment, connection)
);
ALTER TABLE whatsapp_number_health ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_number_health FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON whatsapp_number_health TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON whatsapp_number_health TO taskiem_app;

-- The sweep, across tenants: the businesses with a WhatsApp trigger the
-- customer guard watches (an assistant), a page at a time after a tenant
-- id. Ids only.
GRANT SELECT (guard) ON triggers TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_whatsapp_quality_due(p_after uuid, p_limit int) RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT tr.tenant_id FROM triggers tr
   WHERE tr.tenant_id > p_after
     AND tr.type = 'connector_event' AND tr.connector LIKE 'whatsapp@%' AND tr.guard IS NOT NULL
   ORDER BY tr.tenant_id
   LIMIT p_limit
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_whatsapp_quality_due(uuid, int) OWNER TO taskiem_dispatch;
REVOKE ALL ON FUNCTION taskiem_whatsapp_quality_due(uuid, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_whatsapp_quality_due(uuid, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_whatsapp_quality_due(uuid, int);
REVOKE SELECT (guard) ON triggers FROM taskiem_dispatch;
DROP TABLE whatsapp_number_health;
