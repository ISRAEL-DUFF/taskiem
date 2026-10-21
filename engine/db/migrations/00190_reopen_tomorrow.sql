-- "close shop today" reopens the next morning by itself (no-technical-
-- steps N6): when a closed storefront opens again, and the tenants whose
-- time has come, for the scheduler, which reopens each in its own scope.

-- +goose Up
ALTER TABLE storefronts ADD COLUMN reopen_at timestamptz;
GRANT SELECT (reopen_at) ON storefronts TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_storefronts_to_reopen()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s WHERE s.status = 'closed' AND s.reopen_at <= now() ORDER BY s.reopen_at LIMIT 500
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_storefronts_to_reopen() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefronts_to_reopen() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_storefronts_to_reopen() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_storefronts_to_reopen();
REVOKE SELECT (reopen_at) ON storefronts FROM taskiem_dispatch;
ALTER TABLE storefronts DROP COLUMN reopen_at;
