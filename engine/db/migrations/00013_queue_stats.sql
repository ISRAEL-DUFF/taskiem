-- Queue depth and age for metrics (spec 15.2), across tenants: counts only.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_queue_stats()
RETURNS TABLE (queue text, ready bigint, leased bigint, oldest_ready_seconds double precision)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.queue,
         count(*) FILTER (WHERE t.lease_owner IS NULL AND t.available_at <= now()),
         count(*) FILTER (WHERE t.lease_owner IS NOT NULL),
         COALESCE(EXTRACT(EPOCH FROM now() - min(t.available_at) FILTER (WHERE t.lease_owner IS NULL AND t.available_at <= now())), 0)::double precision
    FROM tasks t GROUP BY t.queue
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_queue_stats() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_queue_stats() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_queue_stats() TO taskiem_app;
