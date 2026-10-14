-- Routing a partner's provider events to its sub-tenants (decision 0031).
--
-- A provider that issues one client (and one webhook subscription) for a
-- partner's many customers sends every customer's events to the partner's
-- tenant. A connector trigger that declares a route (iswallet: the event's
-- wallet_id) is delivered there as usual, then into each sub-tenant of that
-- partner whose connection lists the key (iswallet: wallet_ids).
--
-- connection_route_keys indexes those lists in the clear, per connection,
-- under the tenant's RLS. taskiem_partner_route_event is the one way across:
-- it finds the partner's sub-tenants holding a key, records the routing in
-- the partner's tenant (partner_event_routes), writes it to both audit
-- chains once per event and sub-tenant, and returns the targets. The edge
-- then delivers into each target in that target's own scope, as a delivery
-- to the target's own URL would be.

-- +goose Up

CREATE TABLE connection_route_keys (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  connection_id uuid NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  environment   text NOT NULL,
  connector     text NOT NULL,
  connection    text NOT NULL,                    -- the connection's name
  field         text NOT NULL,                    -- the connection field listing the keys (wallet_ids)
  route_key     text NOT NULL CHECK (length(route_key) BETWEEN 1 AND 255),
  PRIMARY KEY (tenant_id, connection_id, field, route_key)
);
CREATE INDEX connection_route_keys_lookup ON connection_route_keys (connector, environment, field, route_key);
ALTER TABLE connection_route_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE connection_route_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON connection_route_keys TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON connection_route_keys TO taskiem_app;
-- The definer below reads keys across the partner's sub-tenants; these are
-- routing columns only (no credential is ever in this table).
CREATE POLICY dispatch ON connection_route_keys FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON connection_route_keys TO taskiem_dispatch;

-- What the partner received and where it went: one row per event and
-- sub-tenant, or one row with no sub-tenant when no sub-tenant (and not
-- the partner itself) lists the key. Ids and the key only: the event's
-- body stays in the tenants that received it.
CREATE TABLE partner_event_routes (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid NOT NULL REFERENCES tenants(id),   -- the partner
  environment   text NOT NULL,
  connector     text NOT NULL,                          -- iswallet@1
  trigger_name  text NOT NULL,
  event         text NOT NULL,
  dedup         text NOT NULL,                          -- the delivery's dedup key (iswallet: the envelope id)
  route_key     text NOT NULL,
  sub_tenant    uuid REFERENCES tenants(id),
  connection    text,
  status        text NOT NULL CHECK (status IN ('routed', 'unrouted')),
  received_at   timestamptz NOT NULL DEFAULT now(),
  CHECK ((status = 'routed') = (sub_tenant IS NOT NULL))
);
CREATE UNIQUE INDEX partner_event_routes_once ON partner_event_routes (tenant_id, environment, connector, trigger_name, dedup, sub_tenant) NULLS NOT DISTINCT;
CREATE INDEX partner_event_routes_recent ON partner_event_routes (tenant_id, received_at DESC);
ALTER TABLE partner_event_routes ENABLE ROW LEVEL SECURITY;
ALTER TABLE partner_event_routes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON partner_event_routes FOR SELECT TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT ON partner_event_routes TO taskiem_app;
CREATE POLICY dispatch ON partner_event_routes TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT ON partner_event_routes TO taskiem_dispatch;

-- Route one event received by the partner in scope. Returns the sub-tenants
-- to deliver it to (fresh: first time this event reached that sub-tenant),
-- or one row with a null sub-tenant when it matched none. A tenant that is
-- not an active partner routes nothing (no rows), and neither does an event
-- whose key only the partner's own connections list. A repeated event
-- returns the targets recorded the first time, so a replay reaches the same
-- sub-tenants even if a list changed since. Deleted sub-tenants are never
-- targets; suspended ones are (the edge then refuses the delivery with 423
-- so the provider retries, as it would at the sub-tenant's own URL).
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_route_event(p_env text, p_connector text, p_trigger text, p_field text, p_key text, p_event text, p_dedup text)
RETURNS TABLE (sub_tenant uuid, connection text, fresh boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
#variable_conflict use_column
DECLARE
  v_scope uuid[] := taskiem_tenant_scope();
  v_partner uuid;
  v_actor text := 'ingest:' || p_connector || '/' || p_trigger;
  v_detail jsonb := jsonb_build_object('environment', p_env, 'connector', p_connector, 'trigger', p_trigger,
                                       'event', p_event, 'dedup', p_dedup, 'route_key', p_key);
  r record;
  v_any boolean := false;
BEGIN
  IF cardinality(v_scope) <> 1 OR NOT EXISTS (
      SELECT 1 FROM partners p JOIN tenants t ON t.id = p.tenant_id
       WHERE p.tenant_id = v_scope[1] AND t.status = 'active' AND t.parent_id IS NULL) THEN
    RETURN;
  END IF;
  v_partner := v_scope[1];
  -- An event without a key matches no one and is recorded as unrouted.
  IF p_dedup IS NULL OR p_dedup = '' THEN
    RAISE EXCEPTION 'an event without a dedup key cannot be routed' USING ERRCODE = '22023';
  END IF;
  p_key := COALESCE(p_key, '');

  -- Seen before: the same targets, nothing written again.
  IF EXISTS (SELECT 1 FROM partner_event_routes e WHERE e.tenant_id = v_partner AND e.environment = p_env
               AND e.connector = p_connector AND e.trigger_name = p_trigger AND e.dedup = p_dedup) THEN
    RETURN QUERY SELECT e.sub_tenant, e.connection, false FROM partner_event_routes e
      WHERE e.tenant_id = v_partner AND e.environment = p_env AND e.connector = p_connector
        AND e.trigger_name = p_trigger AND e.dedup = p_dedup AND e.sub_tenant IS NOT NULL
      ORDER BY e.sub_tenant, e.connection;
    RETURN;
  END IF;

  FOR r IN
    SELECT DISTINCT ON (k.tenant_id) k.tenant_id, k.connection FROM connection_route_keys k
      JOIN tenants t ON t.id = k.tenant_id
     WHERE t.parent_id = v_partner AND t.status <> 'deleted'
       AND k.connector = split_part(p_connector, '@', 1) AND k.environment = p_env AND k.field = p_field AND k.route_key = p_key
     ORDER BY k.tenant_id, k.connection
  LOOP
    v_any := true;
    INSERT INTO partner_event_routes (tenant_id, environment, connector, trigger_name, event, dedup, route_key, sub_tenant, connection, status)
    VALUES (v_partner, p_env, p_connector, p_trigger, p_event, p_dedup, p_key, r.tenant_id, r.connection, 'routed')
    ON CONFLICT DO NOTHING;
    fresh := FOUND;
    IF fresh THEN
      PERFORM taskiem_audit_append(v_partner, 'system', v_actor, 'partner.event.route', r.tenant_id::text,
        v_detail || jsonb_build_object('sub_tenant', r.tenant_id, 'connection', r.connection));
      PERFORM set_config('app.tenant_scope', '{' || r.tenant_id::text || '}', true);
      PERFORM taskiem_audit_append(r.tenant_id, 'system', 'partner:' || v_partner::text || '/' || v_actor, 'partner.event.route', r.connection,
        v_detail || jsonb_build_object('partner', v_partner, 'connection', r.connection));
      PERFORM set_config('app.tenant_scope', '{' || v_partner::text || '}', true);
    END IF;
    sub_tenant := r.tenant_id;
    connection := r.connection;
    RETURN NEXT;
  END LOOP;
  IF v_any THEN
    RETURN;
  END IF;

  -- The partner's own wallets are not lost events.
  IF EXISTS (SELECT 1 FROM connection_route_keys k WHERE k.tenant_id = v_partner AND k.connector = split_part(p_connector, '@', 1)
               AND k.environment = p_env AND k.field = p_field AND k.route_key = p_key) THEN
    RETURN;
  END IF;
  INSERT INTO partner_event_routes (tenant_id, environment, connector, trigger_name, event, dedup, route_key, status)
  VALUES (v_partner, p_env, p_connector, p_trigger, p_event, p_dedup, p_key, 'unrouted')
  ON CONFLICT DO NOTHING;
  fresh := FOUND;
  IF fresh THEN
    PERFORM taskiem_audit_append(v_partner, 'system', v_actor, 'partner.event.unrouted', p_key, v_detail);
  END IF;
  sub_tenant := NULL;
  connection := NULL;
  RETURN NEXT;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_partner_route_event(text, text, text, text, text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_partner_route_event(text, text, text, text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_partner_route_event(text, text, text, text, text, text, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_partner_route_event(text, text, text, text, text, text, text);
DROP TABLE partner_event_routes;
DROP TABLE connection_route_keys;
