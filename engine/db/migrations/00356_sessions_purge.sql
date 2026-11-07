-- Sign-in sessions were never deleted, only marked revoked, so the table
-- and every client IP in it grew without bound (F77). A session is now
-- deleted 30 days after it expires; a revoked session keeps its expiry, so
-- it goes 30 days after it would have expired. The scheduler calls
-- taskiem_purge_sessions hourly, outside any tenant's scope, in batches.
-- +goose Up
CREATE POLICY dispatch_purge ON sessions FOR DELETE TO taskiem_dispatch USING (true);
GRANT DELETE ON sessions TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_purge_sessions(p_limit int)
RETURNS bigint
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH old AS MATERIALIZED (
    SELECT token_hash FROM sessions
     WHERE expires_at < now() - interval '30 days'
     LIMIT least(greatest(p_limit, 1), 10000)
     FOR UPDATE SKIP LOCKED),
  d AS (DELETE FROM sessions s USING old WHERE s.token_hash = old.token_hash RETURNING 1)
  SELECT count(*) FROM d
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_purge_sessions(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_purge_sessions(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_purge_sessions(int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_purge_sessions(int);
REVOKE DELETE ON sessions FROM taskiem_dispatch;
DROP POLICY dispatch_purge ON sessions;
