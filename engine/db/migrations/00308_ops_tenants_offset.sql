-- The operator console's tenant list pages (decision 0027): taskiem_ops_tenants
-- takes an offset, so "load more" can fetch the next rows. Same columns, same
-- order (newest first, then id), same reads and grants as 00143.
-- +goose Up
DROP FUNCTION taskiem_ops_tenants(text, int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_tenants(p_query text, p_limit int, p_offset int)
RETURNS TABLE (id uuid, name text, parent_id uuid, status text, created_at timestamptz, plan_id text, subscription_status text, pool text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, t.name, t.parent_id, t.status, t.created_at, s.plan_id, s.status, taskiem_tenant_worker_pool(t.id)
    FROM tenants t LEFT JOIN subscriptions s ON s.tenant_id = t.id
   WHERE COALESCE(p_query, '') = '' OR t.id::text = lower(p_query) OR t.name ILIKE '%' || p_query || '%'
   ORDER BY t.created_at DESC, t.id
   LIMIT LEAST(GREATEST(COALESCE(p_limit, 100), 1), 500)
  OFFSET GREATEST(COALESCE(p_offset, 0), 0)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_tenants(text, int, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_tenants(text, int, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_tenants(text, int, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_ops_tenants(text, int, int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_tenants(p_query text, p_limit int)
RETURNS TABLE (id uuid, name text, parent_id uuid, status text, created_at timestamptz, plan_id text, subscription_status text, pool text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, t.name, t.parent_id, t.status, t.created_at, s.plan_id, s.status, taskiem_tenant_worker_pool(t.id)
    FROM tenants t LEFT JOIN subscriptions s ON s.tenant_id = t.id
   WHERE COALESCE(p_query, '') = '' OR t.id::text = lower(p_query) OR t.name ILIKE '%' || p_query || '%'
   ORDER BY t.created_at DESC, t.id
   LIMIT LEAST(GREATEST(COALESCE(p_limit, 100), 1), 500)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_tenants(text, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_tenants(text, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_tenants(text, int) TO taskiem_app;
