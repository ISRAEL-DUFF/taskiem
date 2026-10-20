-- Websites allowed to embed an organisation's storefront and forms
-- (Phase 5, S1; docs/embed-sites.md, storefronts 6.11, forms 6.9).
--
-- The public host's /embed/ pages may be framed only by these sites: their
-- origins go in the pages' frame-ancestors. A site is verified by a DNS TXT
-- record or a file it serves, and re-verified every few hours; one that
-- fails long enough is unverified and stops being allowed, so a domain
-- that changes hands cannot keep framing the business's pages.
-- +goose Up
CREATE TABLE embed_sites (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  origin            text NOT NULL CHECK (origin ~ '^https://[a-z0-9]([a-z0-9.-]*[a-z0-9])?$' AND length(origin) <= 261),
  method            text NOT NULL CHECK (method IN ('dns', 'file')),
  token             text NOT NULL CHECK (token ~ '^taskiem-site-verify=[A-Za-z0-9_-]{32}$'),
  verified_at       timestamptz,
  checked_at        timestamptz,
  check_failures    int NOT NULL DEFAULT 0,
  failing_since     timestamptz,
  last_check_error  text,
  unverified_at     timestamptz,
  created_by        text NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, origin)
);
CREATE INDEX embed_sites_due ON embed_sites (tenant_id, checked_at) WHERE verified_at IS NOT NULL;
ALTER TABLE embed_sites ENABLE ROW LEVEL SECURITY;
ALTER TABLE embed_sites FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON embed_sites TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON embed_sites TO taskiem_app;
CREATE POLICY dispatch ON embed_sites FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, origin, verified_at, checked_at) ON embed_sites TO taskiem_dispatch;

-- Tenants with a verified site due to be checked again.
-- +goose StatementBegin
CREATE FUNCTION taskiem_embed_site_tenants(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT s.tenant_id FROM embed_sites s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.verified_at IS NOT NULL AND COALESCE(s.checked_at, s.verified_at) <= p_before
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_embed_site_tenants(timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_embed_site_tenants(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_embed_site_tenants(timestamptz) TO taskiem_app;

-- The verified origins that may frame a tenant's /embed/ pages, for the
-- public host. An inactive tenant has none.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_embed_origins(p_tenant uuid)
RETURNS text[]
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(array_agg(s.origin ORDER BY s.origin), '{}') FROM embed_sites s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.tenant_id = p_tenant AND s.verified_at IS NOT NULL
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_embed_origins(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_embed_origins(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_embed_origins(uuid) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_embed_origins(uuid);
DROP FUNCTION taskiem_embed_site_tenants(timestamptz);
DROP TABLE embed_sites;
