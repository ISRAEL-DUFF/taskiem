-- +goose Up
-- How a business's public pages look (N13, decision 0042): a theme
-- (clean, bold or editorial), a colour (#rrggbb), and a logo and a cover
-- photo (storefront_media ids, with their URLs copied in when set, as a
-- storefront snapshot copies its logo's). Every public page of the
-- business uses it: the storefront, its forms and status pages, and
-- hosted forms. '{}' is the default look.
ALTER TABLE tenants ADD COLUMN brand jsonb NOT NULL DEFAULT '{}';
-- The public host reads it through taskiem_public_brand, which runs as
-- taskiem_dispatch.
GRANT SELECT (brand) ON tenants TO taskiem_dispatch;

-- A business with a storefront keeps the look it picked there: its
-- layout becomes the theme, its colour name the colour, its logo the
-- logo.
UPDATE tenants t SET brand = jsonb_strip_nulls(jsonb_build_object(
    'theme', CASE s.draft->>'template' WHEN 'bold' THEN 'bold' ELSE 'clean' END,
    'color', CASE s.draft->>'accent'
      WHEN 'blue' THEN '#1d4ed8' WHEN 'red' THEN '#b91c1c' WHEN 'orange' THEN '#c2410c' WHEN 'purple' THEN '#6d28d9'
      WHEN 'pink' THEN '#be185d' WHEN 'teal' THEN '#0f766e' WHEN 'black' THEN '#111827' ELSE '#0b6e4f' END,
    'logo', m.id::text,
    'logo_urls', m.urls))
  FROM storefronts s
  LEFT JOIN storefront_media m ON m.tenant_id = s.tenant_id AND m.moderation <> 'rejected'
   AND m.id::text = s.draft->>'logo'
 WHERE s.tenant_id = t.id;

-- What the public host reads to draw a business's pages.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_brand(p_tenant uuid)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('name', t.name) || t.brand FROM tenants t WHERE t.id = p_tenant AND t.status = 'active'
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_brand(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_brand(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_brand(uuid) TO taskiem_public;

-- A form step's request names its business, so its page takes the
-- business's look (00182, with tenant_id).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_form_request(p_token_hash bytea)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('request_id', r.id, 'title', r.title, 'description', r.description, 'schema', r.schema,
           'verify', r.verify, 'status', CASE WHEN r.status = 'pending' AND r.expires_at <= now() THEN 'expired' ELSE r.status END,
           'business', t.name, 'tenant_id', t.id, 'public_view', encode(r.public_view, 'base64'), 'expires_at', r.expires_at)
    FROM form_requests r JOIN tenants t ON t.id = r.tenant_id AND t.status = 'active'
   WHERE r.token_hash = p_token_hash
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_form_request(p_token_hash bytea)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('request_id', r.id, 'title', r.title, 'description', r.description, 'schema', r.schema,
           'verify', r.verify, 'status', CASE WHEN r.status = 'pending' AND r.expires_at <= now() THEN 'expired' ELSE r.status END,
           'business', t.name, 'public_view', encode(r.public_view, 'base64'), 'expires_at', r.expires_at)
    FROM form_requests r JOIN tenants t ON t.id = r.tenant_id AND t.status = 'active'
   WHERE r.token_hash = p_token_hash
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_public_brand(uuid);
REVOKE SELECT (brand) ON tenants FROM taskiem_dispatch;
ALTER TABLE tenants DROP COLUMN brand;
