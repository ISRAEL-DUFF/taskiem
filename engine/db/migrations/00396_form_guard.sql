-- A form under attack (docs/spec/forms.md 7.3, storefronts 7.5): when a
-- form goes over its own submission limit (every address together, F68)
-- the form host records it, the form is guarded for an hour, and its
-- owners are alerted. While guarded, a public form asks for a phone code
-- before its questions (F68b).
-- - form_endpoints.guarded_since/guarded_until: when the current guard
--   began, and until when it holds (an hour after the last trip).
-- - taskiem_public_form_tripped: the form host's side; it can only guard
--   a live form for an hour, and returns nothing.
-- - taskiem_public_form also says whether the form is guarded.
-- - taskiem_form_guards: the api's side, across businesses: the forms
--   guarded now, for their owners' alerts (once per guard: the alert's
--   dedup key is the form and guarded_since).
-- +goose Up
ALTER TABLE form_endpoints ADD COLUMN guarded_since timestamptz, ADD COLUMN guarded_until timestamptz;
GRANT SELECT (guarded_since, guarded_until), UPDATE (guarded_since, guarded_until) ON form_endpoints TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_tripped(p_endpoint uuid)
RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE form_endpoints
     SET guarded_since = CASE WHEN guarded_until IS NULL OR guarded_until <= now() THEN now() ELSE guarded_since END,
         guarded_until = now() + interval '1 hour'
   WHERE id = p_endpoint AND status = 'live'
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_form_tripped(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_tripped(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_tripped(uuid) TO taskiem_public;

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

-- +goose StatementBegin
CREATE FUNCTION taskiem_form_guards()
RETURNS TABLE (tenant_id uuid, endpoint_id uuid, guarded_since timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT e.tenant_id, e.id, e.guarded_since FROM form_endpoints e
   WHERE e.guarded_until > now()
   ORDER BY e.guarded_since
   LIMIT 500
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_guards() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_guards() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_guards() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_form_guards();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_form(p_public_id text)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('endpoint_id', e.id, 'tenant_id', e.tenant_id, 'environment', e.environment, 'version', e.version, 'status', e.status,
           'open', e.status = 'live' AND (e.opens_at IS NULL OR e.opens_at <= now()) AND (e.closes_at IS NULL OR e.closes_at > now())
             AND (e.max_submissions IS NULL OR e.submissions < e.max_submissions),
           'closed_message', e.closed_message, 'business', t.name,
           'trigger', e.form->'trigger', 'inputs', e.form->'inputs', 'types', e.form->'types', 'reach', e.form->'reach')
    FROM form_endpoints e
    JOIN tenants t ON t.id = e.tenant_id AND t.status = 'active'
   WHERE e.public_id = p_public_id AND e.status <> 'suspended'
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_public_form_tripped(uuid);
REVOKE SELECT (guarded_since, guarded_until), UPDATE (guarded_since, guarded_until) ON form_endpoints FROM taskiem_dispatch;
ALTER TABLE form_endpoints DROP COLUMN guarded_until, DROP COLUMN guarded_since;
