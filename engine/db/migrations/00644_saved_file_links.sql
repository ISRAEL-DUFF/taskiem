-- Links to a business's saved file in the messages its workflows send
-- (H6 review): a run keeps only which saved file it read (its id, nothing
-- to open it with); a message that sends it is given a short link, made
-- when the message is sent and good for a week. The link's token is kept
-- here as a hash only; opening it asks the business's storage for the file
-- then, under the business's scope, so the file is the saved thing's as it
-- is now and a removed thing's link opens nothing.
-- +goose Up
CREATE TABLE saved_file_links (
  token_hash  bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  object_id   uuid NOT NULL REFERENCES saved_objects(id) ON DELETE CASCADE,
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL
);
CREATE INDEX saved_file_links_expiry ON saved_file_links (tenant_id, expires_at);
ALTER TABLE saved_file_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_file_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saved_file_links TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON saved_file_links TO taskiem_app;

-- Opening a link: the business and the saved thing of a token's hash,
-- while it lasts and the business is active; nothing else.
CREATE POLICY dispatch_open ON saved_file_links TO taskiem_dispatch USING (true);
GRANT SELECT (token_hash, tenant_id, object_id, expires_at) ON saved_file_links TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_saved_file_link(p_hash bytea)
RETURNS TABLE (tenant_id uuid, object_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.tenant_id, l.object_id FROM saved_file_links l
    JOIN tenants t ON t.id = l.tenant_id AND t.status = 'active'
   WHERE l.token_hash = p_hash AND l.expires_at > now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_saved_file_link(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_saved_file_link(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_saved_file_link(bytea) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_saved_file_link(bytea);
DROP TABLE saved_file_links;
