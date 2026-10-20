-- The public host: storefronts and hosted forms (decisions 0036, 0037;
-- docs/spec/storefronts.md 8.3).
--
-- Public pages are served by the `public` role, which connects as
-- taskiem_public. That role reads no table at all: it may only execute the
-- narrow functions granted to it here and in later migrations, so a bug in
-- the public server can see published pages and nothing else.
--
-- public_hosts maps a host name (a preview address such as
-- ada-kitchen.<preview domain>, or a business's own domain) to the site it
-- serves. Rows are written by the app when storefronts and forms are set up
-- (Phase 5, S2 and S6); a host serves only once verified_at is set.
-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'taskiem_public') THEN
    CREATE ROLE taskiem_public NOLOGIN NOBYPASSRLS;
  END IF;
END
$$;
-- +goose StatementEnd
GRANT USAGE ON SCHEMA public TO taskiem_public;

CREATE TABLE public_hosts (
  host         text PRIMARY KEY CHECK (host = lower(host) AND length(host) <= 253
                 AND host ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'),
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  kind         text NOT NULL CHECK (kind IN ('storefront', 'form')),
  site_id      uuid NOT NULL,
  status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
  verified_at  timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX public_hosts_tenant ON public_hosts (tenant_id);
CREATE INDEX public_hosts_site ON public_hosts (site_id);

ALTER TABLE public_hosts ENABLE ROW LEVEL SECURITY;
ALTER TABLE public_hosts FORCE ROW LEVEL SECURITY;
-- A tenant sees its own hosts; writes come with the features that make them.
CREATE POLICY tenant_isolation ON public_hosts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT ON public_hosts TO taskiem_app;
-- The lookup below runs as taskiem_dispatch, which reads every host.
CREATE POLICY dispatch ON public_hosts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON public_hosts TO taskiem_dispatch;

-- The site a host serves, if it is verified. A host whose tenant is not
-- active answers as suspended, so the page says it is unavailable rather
-- than vanishing. Called by the public server for every host it has not
-- cached, and by the proxy's on-demand TLS check before it asks for a
-- certificate.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_host(p_host text)
RETURNS TABLE (tenant_id uuid, kind text, site_id uuid, status text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT h.tenant_id, h.kind, h.site_id,
         CASE WHEN t.status = 'active' THEN h.status ELSE 'suspended' END
    FROM public_hosts h
    JOIN tenants t ON t.id = h.tenant_id
   WHERE h.host = lower(p_host) AND h.verified_at IS NOT NULL
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_host(text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_host(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_host(text) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_host(text);
DROP TABLE public_hosts;
REVOKE USAGE ON SCHEMA public FROM taskiem_public;
