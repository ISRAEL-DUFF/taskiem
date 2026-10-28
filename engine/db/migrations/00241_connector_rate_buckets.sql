-- Connectors' rate limits (spec 6.2; engine findings, Atlas part C): a
-- token bucket per connection (or per tenant) that every worker draws from,
-- so a provider's quota holds however many workers call it. Manifests
-- declared rate_limit, but nothing kept to it.

-- +goose Up
CREATE TABLE connector_rate_buckets (
  tenant_id   uuid NOT NULL,
  bucket      text NOT NULL,   -- connector id, and environment and connection for scope connection
  tokens      double precision NOT NULL,
  updated_at  timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, bucket)
);
ALTER TABLE connector_rate_buckets ENABLE ROW LEVEL SECURITY;
ALTER TABLE connector_rate_buckets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON connector_rate_buckets TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON connector_rate_buckets TO taskiem_app;

-- +goose Down
DROP TABLE connector_rate_buckets;
