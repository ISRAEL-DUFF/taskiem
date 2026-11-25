-- +goose Up
-- Forms that sell L1 (docs/spec/forms-that-sell.md §5.1): Sell something
-- with this form turns a workflow's hosted form into an order action of
-- the storefront. The form keeps its address: its endpoint names the
-- action, and the form host sends /f/<id> (and /embed/f/<id>) to the
-- action's own address on the storefront while the action is live there.
ALTER TABLE form_endpoints ADD COLUMN action_id uuid REFERENCES storefront_actions(id) ON DELETE SET NULL;
GRANT SELECT (action_id) ON form_endpoints TO taskiem_dispatch;

-- +goose StatementBegin
-- The form host's read of a form, with where a form that now sells is
-- served: the storefront's published name, its own domain if it has a
-- verified one, and the action's path. Nothing else of the storefront.
CREATE OR REPLACE FUNCTION taskiem_public_form(p_public_id text)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('endpoint_id', e.id, 'tenant_id', e.tenant_id, 'environment', e.environment, 'version', e.version, 'status', e.status,
           'open', e.status = 'live' AND (e.opens_at IS NULL OR e.opens_at <= now()) AND (e.closes_at IS NULL OR e.closes_at > now())
             AND (e.max_submissions IS NULL OR e.submissions < e.max_submissions),
           'closed_message', e.closed_message, 'business', t.name,
           'trigger', e.form->'trigger', 'inputs', e.form->'inputs', 'types', e.form->'types', 'reach', e.form->'reach',
           'guarded', COALESCE(e.guarded_until > now(), false),
           'sells', (SELECT jsonb_build_object('slug', COALESCE(s.published_slug, s.slug),
                                               'domain', taskiem_public_storefront_domain(s.id),
                                               'path', CASE WHEN f.slug = '' THEN '' ELSE '/' || f.slug END || '/a/' || a.slug)
                       FROM storefront_actions a
                       JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
                       JOIN storefront_fronts f ON f.id = a.front_id AND f.enabled
                      WHERE a.id = e.action_id AND a.enabled AND s.tenant_id = e.tenant_id),
           'sold', e.action_id IS NOT NULL)
    FROM form_endpoints e
    JOIN tenants t ON t.id = e.tenant_id AND t.status = 'active'
   WHERE e.public_id = p_public_id AND e.status <> 'suspended'
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_form(p_public_id text)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('endpoint_id', e.id, 'tenant_id', e.tenant_id, 'environment', e.environment, 'version', e.version, 'status', e.status,
           'open', e.status = 'live' AND (e.opens_at IS NULL OR e.opens_at <= now()) AND (e.closes_at IS NULL OR e.closes_at > now())
             AND (e.max_submissions IS NULL OR e.submissions < e.max_submissions),
           'closed_message', e.closed_message, 'business', t.name,
           'trigger', e.form->'trigger', 'inputs', e.form->'inputs', 'types', e.form->'types', 'reach', e.form->'reach',
           'guarded', COALESCE(e.guarded_until > now(), false))
    FROM form_endpoints e
    JOIN tenants t ON t.id = e.tenant_id AND t.status = 'active'
   WHERE e.public_id = p_public_id AND e.status <> 'suspended'
$$;
-- +goose StatementEnd
REVOKE SELECT (action_id) ON form_endpoints FROM taskiem_dispatch;
ALTER TABLE form_endpoints DROP COLUMN action_id;
