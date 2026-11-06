-- Partner events reach only the connections the partner provisioned (F3).
-- taskiem_partner_route_event matched every sub-tenant's route keys, and a
-- sub-tenant writes its own: an owner of one customer could list a
-- sibling's wallet_ids on a connection it made itself and receive that
-- sibling's payment events (and, by getting itself throttled, hold them
-- back for everyone). A target is now a sub-tenant whose active connection
-- the partner provisioned (provisioned_by 'partner:<partner>/...') lists
-- the key. A tenant's own wallet_ids still limit its own calls (decision
-- 0031 point 1); they no longer bring it anyone's events.
-- +goose Up
GRANT SELECT (provisioned_by) ON connections TO taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_partner_route_event(p_env text, p_connector text, p_trigger text, p_field text, p_key text, p_event text, p_dedup text)
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
      JOIN connections c ON c.id = k.connection_id AND c.tenant_id = k.tenant_id
     WHERE t.parent_id = v_partner AND t.status <> 'deleted'
       AND c.status = 'active' AND c.provisioned_by LIKE 'partner:' || v_partner::text || '/%'
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
