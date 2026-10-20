-- Storefronts: the page (Phase 5, S2; docs/spec/storefronts.md 6.1, 6.2,
-- 6.7, 8.1; docs/storefronts.md).
--
-- One storefront per tenant. The owner edits a draft; publishing writes an
-- immutable snapshot (the page's model) and the public host serves the
-- newest. The catalogue's rows live in a data table on Xenibase; the
-- public host reads a copy of their public columns kept here
-- (storefront_catalogue), so a page view never reaches Xenibase.
-- +goose Up
CREATE TABLE storefronts (
  id                  uuid PRIMARY KEY,
  tenant_id           uuid NOT NULL UNIQUE REFERENCES tenants(id),
  slug                text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$' AND length(slug) BETWEEN 3 AND 30),
  status              text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'closed')),
  draft               jsonb NOT NULL DEFAULT '{}',
  catalogue_table     uuid REFERENCES data_tables(id) ON DELETE SET NULL,
  catalogue_columns   jsonb NOT NULL DEFAULT '{}',
  published_version   int NOT NULL DEFAULT 0,
  published_at        timestamptz,
  closed_message      text,
  catalogue_synced_at timestamptz,
  catalogue_error     text,
  created_by          text NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE storefronts ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefronts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefronts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON storefronts TO taskiem_app;
CREATE POLICY dispatch ON storefronts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, status, published_version, closed_message, catalogue_synced_at) ON storefronts TO taskiem_dispatch;

-- Published versions: the page's model, content-hashed. Never changed.
CREATE TABLE storefront_snapshots (
  storefront_id  uuid NOT NULL REFERENCES storefronts(id),
  version        int NOT NULL CHECK (version > 0),
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  model          jsonb NOT NULL,
  hash           bytea NOT NULL CHECK (length(hash) = 32),
  created_by     text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (storefront_id, version)
);
ALTER TABLE storefront_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_snapshots TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON storefront_snapshots TO taskiem_app;
CREATE POLICY dispatch ON storefront_snapshots FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (storefront_id, version, model) ON storefront_snapshots TO taskiem_dispatch;

-- The catalogue's public columns, copied from the data table.
CREATE TABLE storefront_catalogue (
  storefront_id  uuid NOT NULL REFERENCES storefronts(id),
  row_id         text NOT NULL,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  position       int NOT NULL,
  item           jsonb NOT NULL,
  synced_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (storefront_id, row_id)
);
ALTER TABLE storefront_catalogue ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_catalogue FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_catalogue TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_catalogue TO taskiem_app;
CREATE POLICY dispatch ON storefront_catalogue FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (storefront_id, position, item) ON storefront_catalogue TO taskiem_dispatch;

-- Photos and logos stored for the storefront (engine/media), with the
-- moderation hook's verdict: rejected media is never shown.
CREATE TABLE storefront_media (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  hash        text NOT NULL,
  width       int NOT NULL,
  height      int NOT NULL,
  type        text NOT NULL,
  urls        jsonb NOT NULL,
  paths       text[] NOT NULL,
  moderation  text NOT NULL DEFAULT 'unreviewed' CHECK (moderation IN ('unreviewed', 'approved', 'rejected')),
  moderation_reason text,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, hash)
);
ALTER TABLE storefront_media ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_media FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_media TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON storefront_media TO taskiem_app;

-- Preview hosts are the platform's own names, claimed through the function
-- below; a business's own domain is added and verified separately (S6).
ALTER TABLE public_hosts ADD COLUMN preview boolean NOT NULL DEFAULT false;
GRANT INSERT, DELETE ON public_hosts TO taskiem_dispatch;
CREATE POLICY dispatch_write ON public_hosts FOR ALL TO taskiem_dispatch USING (true) WITH CHECK (true);

-- Claims a storefront's preview host for it, in the caller's scope, and
-- releases the one it had. The app reads public_hosts but writes it only
-- here, so a tenant can claim only a free host for its own storefront.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_claim_host(p_storefront uuid, p_host text)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
BEGIN
  SELECT s.tenant_id INTO t FROM storefronts s WHERE s.id = p_storefront AND s.tenant_id = ANY (taskiem_tenant_scope());
  IF t IS NULL THEN
    RAISE EXCEPTION 'no such storefront' USING ERRCODE = 'P0002';
  END IF;
  IF EXISTS (SELECT 1 FROM public_hosts h WHERE h.host = lower(p_host) AND h.site_id <> p_storefront) THEN
    RETURN false;
  END IF;
  DELETE FROM public_hosts h WHERE h.site_id = p_storefront AND h.kind = 'storefront' AND h.host <> lower(p_host) AND h.preview;
  INSERT INTO public_hosts (host, tenant_id, kind, site_id, verified_at, preview)
    VALUES (lower(p_host), t, 'storefront', p_storefront, now(), true)
    ON CONFLICT (host) DO NOTHING;
  RETURN true;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_storefront_claim_host(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefront_claim_host(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_storefront_claim_host(uuid, text) TO taskiem_app;

-- What the public host renders: a published storefront's newest snapshot,
-- with its status (a closed storefront shows its message; a draft or an
-- inactive tenant's storefront is not served).
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_storefront(p_storefront uuid)
RETURNS TABLE (status text, version int, model jsonb, closed_message text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.status, s.published_version, n.model, s.closed_message
    FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
    JOIN storefront_snapshots n ON n.storefront_id = s.id AND n.version = s.published_version
   WHERE s.id = p_storefront AND s.status IN ('published', 'closed')
$$;
-- +goose StatementEnd

-- The catalogue's items, in order.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_storefront_catalogue(p_storefront uuid)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(jsonb_agg(c.item ORDER BY c.position), '[]')
    FROM storefront_catalogue c
    JOIN storefronts s ON s.id = c.storefront_id AND s.status IN ('published', 'closed')
   WHERE c.storefront_id = p_storefront
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_storefront(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_storefront_catalogue(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_storefront(uuid), taskiem_public_storefront_catalogue(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_storefront(uuid), taskiem_public_storefront_catalogue(uuid) TO taskiem_public;

-- Published storefronts due a catalogue refresh, for the reconciler.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefronts_due(p_before timestamptz)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.status IN ('published', 'closed') AND COALESCE(s.catalogue_synced_at, '-infinity') <= p_before
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_storefronts_due(timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefronts_due(timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_storefronts_due(timestamptz) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_storefronts_due(timestamptz);
DROP FUNCTION taskiem_public_storefront_catalogue(uuid);
DROP FUNCTION taskiem_public_storefront(uuid);
DROP FUNCTION taskiem_storefront_claim_host(uuid, text);
DROP POLICY dispatch_write ON public_hosts;
REVOKE INSERT, DELETE ON public_hosts FROM taskiem_dispatch;
ALTER TABLE public_hosts DROP COLUMN preview;
DROP TABLE storefront_media;
DROP TABLE storefront_catalogue;
DROP TABLE storefront_snapshots;
DROP TABLE storefronts;
