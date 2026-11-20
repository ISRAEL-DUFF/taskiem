-- Saved things, after S1's review (docs/spec/saved-objects.md): uploads
-- never claimed are purged a day on by the periodic sweep, not only when
-- their owner next uploads; a business's answer where each was kept in its
-- storage, so its copy goes too.
-- +goose Up
CREATE POLICY dispatch_purge ON saved_files FOR DELETE TO taskiem_dispatch
  USING (object_id IS NULL AND created_at < now() - interval '1 day');
CREATE POLICY dispatch_read_stale ON saved_files FOR SELECT TO taskiem_dispatch
  USING (object_id IS NULL AND created_at < now() - interval '1 day');
GRANT SELECT (id, tenant_id, object_id, created_at, path), DELETE ON saved_files TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_saved_uploads_purge()
RETURNS TABLE (tenant_id uuid, path text) LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  RETURN QUERY WITH gone AS (
    DELETE FROM saved_files f WHERE f.object_id IS NULL AND f.created_at < now() - interval '1 day'
    RETURNING f.tenant_id, f.path
  ) SELECT g.tenant_id, g.path FROM gone g WHERE g.path IS NOT NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_saved_uploads_purge() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_saved_uploads_purge() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_saved_uploads_purge() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_saved_uploads_purge();
REVOKE SELECT (id, tenant_id, object_id, created_at, path), DELETE ON saved_files FROM taskiem_dispatch;
DROP POLICY dispatch_read_stale ON saved_files;
DROP POLICY dispatch_purge ON saved_files;
