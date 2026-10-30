-- Connecting from chat (docs/spec/chat-way-in.md 7, decision 0058).
--
-- Chat sends a one-time connect link for one service, one business and
-- one person: https://<public url>/c/<token>. The token (24 random bytes)
-- is kept only as its SHA-256 and is the authority, for that one
-- connection only, for 15 minutes; it is spent when the connection is
-- made. A link is the business's: it lives in the tenant's records under
-- its row-level security. The page that opens it has no tenant scope yet,
-- so taskiem_connect_link_tenant finds which business a live token is
-- for, and nothing else.
-- +goose Up
CREATE TABLE connect_links (
  token_hash        bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  user_id           uuid NOT NULL REFERENCES users(id),
  -- The connector (its manifest id), and the connection it makes.
  connector         text NOT NULL CHECK (connector ~ '^[a-z0-9][a-z0-9._-]{0,63}$'),
  environment       text NOT NULL DEFAULT 'prod',
  -- The chat that asked, told when the connection is made.
  channel           text NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'slack')),
  address           text NOT NULL CHECK (length(address) BETWEEN 1 AND 64),
  -- What it was for: the build or draft to carry on with.
  workflow_id       uuid,
  -- An OAuth consent started from it, under this connection name.
  oauth_name        text,
  oauth_started_at  timestamptz,
  expires_at        timestamptz NOT NULL,
  spent_at          timestamptz,
  connection_id     uuid,
  created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX connect_links_tenant ON connect_links (tenant_id, created_at DESC);
CREATE INDEX connect_links_live ON connect_links (address, expires_at) WHERE spent_at IS NULL;
ALTER TABLE connect_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE connect_links FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON connect_links TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON connect_links TO taskiem_app;
CREATE POLICY dispatch ON connect_links FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (token_hash, tenant_id, address, expires_at, spent_at) ON connect_links TO taskiem_dispatch;

-- +goose StatementBegin
-- The business a live, unspent connect link is for.
CREATE FUNCTION taskiem_connect_link_tenant(p_hash bytea)
RETURNS uuid LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.tenant_id FROM connect_links l WHERE l.token_hash = p_hash AND l.spent_at IS NULL AND l.expires_at > now()
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Whether a chat address has a connect link waiting: while it does, a
-- token-shaped message from it is taken for a key (the pasted-key guard).
CREATE FUNCTION taskiem_connect_pending(p_address text)
RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM connect_links l WHERE l.address = p_address AND l.spent_at IS NULL AND l.expires_at > now())
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_connect_link_tenant(bytea)', 'taskiem_connect_pending(text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- The key form on WhatsApp (taskiem_connect).
ALTER TABLE whatsapp_flows DROP CONSTRAINT whatsapp_flows_kind_check;
ALTER TABLE whatsapp_flows ADD CONSTRAINT whatsapp_flows_kind_check CHECK (kind IN ('inputs', 'pin', 'public', 'pin_set', 'pin_wake', 'connect'));

-- +goose Down
DELETE FROM whatsapp_flows WHERE kind = 'connect';
ALTER TABLE whatsapp_flows DROP CONSTRAINT whatsapp_flows_kind_check;
ALTER TABLE whatsapp_flows ADD CONSTRAINT whatsapp_flows_kind_check CHECK (kind IN ('inputs', 'pin', 'public', 'pin_set', 'pin_wake'));
DROP FUNCTION taskiem_connect_pending(text);
DROP FUNCTION taskiem_connect_link_tenant(bytea);
DROP TABLE connect_links;
