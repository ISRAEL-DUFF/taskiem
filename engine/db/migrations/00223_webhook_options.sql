-- Webhook trigger options (wd-v1 webhook triggers; docs/contracts/wd-v1.md#webhook-triggers).
--
-- triggers.auth gains basic (a user name and password, the environment
-- secret holding "user:password") and jwt (a token signed with the
-- environment secret, a shared key or a PEM public key). A webhook's
-- methods, jwt settings and CORS origins are kept in triggers.options,
-- the column connector triggers already use for theirs.
--
-- webhook_listens: "Listen for a test call" in the editor. A person asks
-- for a test address for a workflow they are building; the first request
-- to it within two minutes is kept, as the run would see it, so they can
-- see what the sender sends before publishing. The address carries a
-- random token (hashed here, so a database read cannot call it); the
-- capture is sealed under the tenant's key, since it may hold personal
-- data, and rows are deleted an hour after they expire, when the tenant
-- next starts a listen. A listen is never a run: nothing it captures
-- starts anything.

-- +goose Up
ALTER TABLE triggers DROP CONSTRAINT triggers_auth_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_auth_check CHECK (auth IN ('hmac', 'bearer', 'basic', 'jwt', 'none'));

CREATE TABLE webhook_listens (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  workflow_id  uuid NOT NULL,
  environment  text NOT NULL,                                          -- whose webhook secret checks the call
  token_hash   bytea NOT NULL CHECK (length(token_hash) = 32),        -- SHA-256 of the address's token
  config       jsonb NOT NULL,                                         -- the webhook trigger config being built
  secret_name  text NOT NULL,                                          -- webhook_<WD id>
  person       text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  captured_at  timestamptz,
  capture      jsonb,                                                  -- the request, sealed
  CHECK ((captured_at IS NULL) = (capture IS NULL))
);
CREATE INDEX webhook_listens_expiry ON webhook_listens (tenant_id, expires_at);
ALTER TABLE webhook_listens ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_listens FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webhook_listens TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON webhook_listens TO taskiem_app;
GRANT UPDATE (captured_at, capture, expires_at) ON webhook_listens TO taskiem_app;

-- +goose Down
DROP TABLE webhook_listens;
-- Never left open: a webhook the old code cannot check is unregistered
-- (its address answers 404 until the workflow is published again).
DELETE FROM triggers WHERE auth IN ('basic', 'jwt');
ALTER TABLE triggers DROP CONSTRAINT triggers_auth_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_auth_check CHECK (auth IN ('hmac', 'bearer', 'none'));
