-- A business's own domain for its storefront (Phase 5, S6;
-- docs/spec/storefronts.md 6.6, docs/storefronts.md#your-own-domain).
--
-- The owner adds a domain it already has (adakitchen.ng, shop.example.com)
-- and proves it with a DNS TXT record, as embed sites are proven. Once
-- proven, the domain (and its www. twin, for a registrable domain) becomes
-- a public_hosts row serving the storefront, so the proxy's /tls/ask
-- answers 200 and a certificate is issued on demand. Any number of
-- organisations may add the same domain; the first to prove it serves it.
-- The scheduler proves verified domains again every few hours; one that
-- fails long enough is unverified and its hosts are released.
--
-- 00185 is the S8 migration (moderation and plans), built alongside.
-- +goose Up
CREATE TABLE storefront_domains (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  storefront_id     uuid NOT NULL REFERENCES storefronts(id),
  host              text NOT NULL CHECK (host = lower(host) AND length(host) <= 253
                      AND host ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'),
  -- The www. twin of a registrable domain (or the domain of a www. host):
  -- served too, redirecting to the primary.
  twin              text CHECK (twin IS NULL OR (twin = lower(twin) AND length(twin) <= 253
                      AND twin ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$')),
  token             text NOT NULL CHECK (token ~ '^taskiem-domain-verify=[A-Za-z0-9_-]{32}$'),
  is_primary        boolean NOT NULL DEFAULT false,
  verified_at       timestamptz,
  checked_at        timestamptz,
  check_failures    int NOT NULL DEFAULT 0,
  failing_since     timestamptz,
  last_check_error  text,
  unverified_at     timestamptz,
  created_by        text NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (storefront_id, host)
);
CREATE UNIQUE INDEX storefront_domains_primary ON storefront_domains (storefront_id) WHERE is_primary;
CREATE INDEX storefront_domains_due ON storefront_domains (tenant_id, checked_at) WHERE verified_at IS NOT NULL;
ALTER TABLE storefront_domains ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_domains FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_domains TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_domains TO taskiem_app;
CREATE POLICY dispatch ON storefront_domains FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, storefront_id, host, twin, is_primary, verified_at, checked_at) ON storefront_domains TO taskiem_dispatch;

-- Tenants with a verified domain due to be checked again.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_domain_tenants(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT d.tenant_id FROM storefront_domains d
    JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active'
   WHERE d.verified_at IS NOT NULL AND COALESCE(d.checked_at, d.verified_at) <= p_before
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_storefront_domain_tenants(timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefront_domain_tenants(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_storefront_domain_tenants(timestamptz) TO taskiem_app;

-- Serves a proven domain (and its twin) for its storefront, in the
-- caller's scope. False when another site already serves the domain: the
-- first organisation to prove a domain keeps it until it is unverified or
-- removed. A twin another site serves is left to it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_claim_domain(p_domain uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  d record;
BEGIN
  SELECT x.tenant_id, x.storefront_id, x.host, x.twin INTO d FROM storefront_domains x
   WHERE x.id = p_domain AND x.tenant_id = ANY (taskiem_tenant_scope());
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such domain' USING ERRCODE = 'P0002';
  END IF;
  IF EXISTS (SELECT 1 FROM public_hosts h WHERE h.host = d.host AND h.site_id <> d.storefront_id) THEN
    RETURN false;
  END IF;
  INSERT INTO public_hosts (host, tenant_id, kind, site_id, verified_at, preview)
    VALUES (d.host, d.tenant_id, 'storefront', d.storefront_id, now(), false)
    ON CONFLICT (host) DO NOTHING;
  IF d.twin IS NOT NULL THEN
    INSERT INTO public_hosts (host, tenant_id, kind, site_id, verified_at, preview)
      VALUES (d.twin, d.tenant_id, 'storefront', d.storefront_id, now(), false)
      ON CONFLICT (host) DO NOTHING;
  END IF;
  RETURN true;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_storefront_claim_domain(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefront_claim_domain(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_storefront_claim_domain(uuid) TO taskiem_app;

-- Stops serving a storefront's own domain (and its twin), in the caller's
-- scope: removed or unverified. Preview hosts are never touched here.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_release_domain(p_storefront uuid, p_hosts text[])
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM storefronts s WHERE s.id = p_storefront AND s.tenant_id = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'no such storefront' USING ERRCODE = 'P0002';
  END IF;
  DELETE FROM public_hosts h WHERE h.site_id = p_storefront AND h.kind = 'storefront' AND NOT h.preview AND h.host = ANY (p_hosts);
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_storefront_release_domain(uuid, text[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefront_release_domain(uuid, text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_storefront_release_domain(uuid, text[]) TO taskiem_app;

-- The public host's: the storefront's primary own domain while it is
-- served and the page is live (published or closed), so its other hosts
-- (the preview address, the www. twin, other domains) redirect to it.
-- NULL otherwise: the preview address serves.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_storefront_domain(p_storefront uuid)
RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.host FROM storefront_domains d
    JOIN storefronts s ON s.id = d.storefront_id AND s.status IN ('published', 'closed')
    JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active'
    JOIN public_hosts h ON h.host = d.host AND h.site_id = d.storefront_id AND h.verified_at IS NOT NULL AND h.status = 'active'
   WHERE d.storefront_id = p_storefront AND d.verified_at IS NOT NULL
   ORDER BY d.is_primary DESC, d.verified_at, d.host
   LIMIT 1
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_storefront_domain(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_storefront_domain(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_storefront_domain(uuid) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_storefront_domain(uuid);
DROP FUNCTION taskiem_storefront_release_domain(uuid, text[]);
DROP FUNCTION taskiem_storefront_claim_domain(uuid);
DROP FUNCTION taskiem_storefront_domain_tenants(timestamptz);
DELETE FROM public_hosts WHERE kind = 'storefront' AND NOT preview;
DROP TABLE storefront_domains;
