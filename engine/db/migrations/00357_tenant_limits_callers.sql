-- taskiem_set_tenant_limits wrote any limit of any tenant in the caller's
-- scope, so any tenant's transaction could raise its own plan limits: the
-- partner ceiling existed only in Go (F78). Its callers are split:
--
--   - taskiem_set_tenant_limits is the partner API's: it sets a sub-tenant's
--     limits only (a top-level organisation's are an operator's), and no
--     higher than the partner's own explicit limits (Go also checks the
--     partner's effective limits, which the database does not know, and
--     caps a sub-tenant's limits to them when it reads them).
--   - taskiem_ops_set_tenant_limits is the operators' (taskiem tenants
--     limits): any tenant in the caller's scope, as the other operator
--     functions (taskiem_set_partner, taskiem_put_plan). Only
--     runtime.Store.SetLimits calls it, and only the CLI calls that
--     (TestOperatorLimitsCallers).
--
-- Both write through taskiem_limits_apply, which runs as its caller (the
-- definers run as taskiem_dispatch) and which taskiem_app may not execute.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_limits_apply(p_tenant uuid, p_values jsonb, p_by text)
RETURNS void
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  k text;
  known text[];
  sets text;
BEGIN
  SELECT array_agg(a.attname::text) INTO known FROM pg_attribute a
   WHERE a.attrelid = 'public.tenant_limits'::regclass AND a.attnum > 0 AND NOT a.attisdropped
     AND a.attname NOT IN ('tenant_id', 'updated_by', 'updated_at');
  FOR k IN SELECT jsonb_object_keys(p_values) LOOP
    IF NOT (k = ANY (known)) THEN
      RAISE EXCEPTION 'unknown limit %', k USING ERRCODE = '22023';
    END IF;
  END LOOP;
  INSERT INTO tenant_limits (tenant_id, updated_by) VALUES (p_tenant, p_by)
    ON CONFLICT (tenant_id) DO UPDATE SET updated_by = EXCLUDED.updated_by, updated_at = now();
  -- Each named limit takes its value (null: back to the platform default),
  -- cast to its column's type; the others stay.
  SELECT string_agg(format('%I = CASE WHEN $2 ? %L THEN v.%I ELSE l.%I END', c, c, c, c), ', ') INTO sets FROM unnest(known) c;
  EXECUTE format('UPDATE tenant_limits l SET %s FROM jsonb_populate_record(NULL::public.tenant_limits, $2) v WHERE l.tenant_id = $1', sets)
    USING p_tenant, p_values;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_limits_apply(uuid, jsonb, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_limits_apply(uuid, jsonb, text) FROM PUBLIC;

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_set_tenant_limits(p_tenant uuid, p_values jsonb, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  PERFORM taskiem_limits_apply(p_tenant, p_values, p_by);
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_set_tenant_limits(uuid, jsonb, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_set_tenant_limits(uuid, jsonb, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_set_tenant_limits(uuid, jsonb, text) TO taskiem_app;

-- A sub-tenant's limit may not pass its partner's explicit one, read as
-- runtime.Limits.Exceeds does: a partner's 0 is no limit (any value
-- passes), except container_minutes_monthly, whose 0 turns the feature
-- off; under a partner's positive limit, 0 (no limit) is above it. A null
-- (the partner's own limit applies) always passes.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_set_tenant_limits(p_tenant uuid, p_values jsonb, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_parent uuid;
  v_over text;
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  SELECT parent_id INTO v_parent FROM tenants WHERE id = p_tenant;
  IF v_parent IS NULL THEN
    RAISE EXCEPTION 'the limits of an organisation that is not a sub-tenant are set by an operator' USING ERRCODE = '42501';
  END IF;
  IF jsonb_typeof(p_values) <> 'object' THEN
    RAISE EXCEPTION 'limits are an object' USING ERRCODE = '22023';
  END IF;
  SELECT string_agg(r.key, ', ' ORDER BY r.key) INTO v_over
    FROM jsonb_each(p_values) r
    JOIN jsonb_each(jsonb_strip_nulls((SELECT to_jsonb(pl) - 'tenant_id' - 'updated_by' - 'updated_at' FROM tenant_limits pl WHERE pl.tenant_id = v_parent))) c
      ON c.key = r.key
   WHERE jsonb_typeof(r.value) <> 'null'
     AND CASE WHEN r.key = 'container_minutes_monthly' THEN (r.value::text)::numeric > (c.value::text)::numeric
              ELSE (c.value::text)::numeric > 0 AND ((r.value::text)::numeric <= 0 OR (r.value::text)::numeric > (c.value::text)::numeric)
         END;
  IF v_over IS NOT NULL THEN
    RAISE EXCEPTION 'a sub-tenant''s limits cannot exceed its partner''s (%)', v_over USING ERRCODE = '42501';
  END IF;
  PERFORM taskiem_limits_apply(p_tenant, p_values, p_by);
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_set_tenant_limits(p_tenant uuid, p_values jsonb, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  k text;
  known text[];
  sets text;
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  SELECT array_agg(a.attname::text) INTO known FROM pg_attribute a
   WHERE a.attrelid = 'public.tenant_limits'::regclass AND a.attnum > 0 AND NOT a.attisdropped
     AND a.attname NOT IN ('tenant_id', 'updated_by', 'updated_at');
  FOR k IN SELECT jsonb_object_keys(p_values) LOOP
    IF NOT (k = ANY (known)) THEN
      RAISE EXCEPTION 'unknown limit %', k USING ERRCODE = '22023';
    END IF;
  END LOOP;
  INSERT INTO tenant_limits (tenant_id, updated_by) VALUES (p_tenant, p_by)
    ON CONFLICT (tenant_id) DO UPDATE SET updated_by = EXCLUDED.updated_by, updated_at = now();
  SELECT string_agg(format('%I = CASE WHEN $2 ? %L THEN v.%I ELSE l.%I END', c, c, c, c), ', ') INTO sets FROM unnest(known) c;
  EXECUTE format('UPDATE tenant_limits l SET %s FROM jsonb_populate_record(NULL::public.tenant_limits, $2) v WHERE l.tenant_id = $1', sets)
    USING p_tenant, p_values;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_ops_set_tenant_limits(uuid, jsonb, text);
DROP FUNCTION taskiem_limits_apply(uuid, jsonb, text);
