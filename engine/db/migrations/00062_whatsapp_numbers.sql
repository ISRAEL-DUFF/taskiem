-- Tenants' own WhatsApp numbers (spec 11.4). A tenant may connect its own
-- WhatsApp Business number: its people then hear from, and write to, that
-- number for the tenant, and the tenant may offer a public self-service
-- menu on it. The credentials (access token, app secret, verify token) are
-- in the tenant's vault, never here. Inbound webhooks carry only the phone
-- number id, so one function maps it to its tenant.

-- +goose Up
CREATE TABLE whatsapp_numbers (
  phone_number_id  text PRIMARY KEY CHECK (phone_number_id ~ '^[0-9]{5,32}$'),
  tenant_id        uuid NOT NULL UNIQUE REFERENCES tenants(id),
  waba_id          text NOT NULL CHECK (waba_id ~ '^[0-9]{5,32}$'),
  display_number   text NOT NULL CHECK (display_number ~ '^\+[1-9][0-9]{6,14}$'),
  -- own_app: the number is on the tenant's own Meta app, whose webhooks
  -- come to /channels/whatsapp/n/<phone number id> signed with its app
  -- secret; otherwise on Taskiem's app (embedded signup), whose webhooks
  -- come to /channels/whatsapp.
  own_app          boolean NOT NULL DEFAULT true,
  source           text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'embedded_signup')),
  status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  public_menu      boolean NOT NULL DEFAULT false,
  created_by       text NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

-- The 24-hour window per business number: a person writing to a tenant's
-- number opens that number's window, not the shared number's.
CREATE TABLE whatsapp_own_contacts (
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  phone_number_id  text NOT NULL,
  number           text NOT NULL CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  last_inbound_at  timestamptz,
  PRIMARY KEY (phone_number_id, number)
);

-- Workflows a tenant offers on its public menu (no sign-in).
CREATE TABLE whatsapp_public_workflows (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  workflow_id  uuid NOT NULL,
  label        text NOT NULL CHECK (length(label) BETWEEN 1 AND 24),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, workflow_id)
);

-- A public menu's conversation with an unbound number.
CREATE TABLE whatsapp_public_sessions (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  number      text NOT NULL CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  state       text NOT NULL CHECK (state IN ('collecting_input', 'awaiting_confirmation')),
  data        jsonb NOT NULL DEFAULT '{}',
  expires_at  timestamptz NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, number)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['whatsapp_numbers', 'whatsapp_own_contacts', 'whatsapp_public_workflows', 'whatsapp_public_sessions'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd
CREATE POLICY dispatch ON whatsapp_numbers TO taskiem_dispatch USING (true);
GRANT SELECT ON whatsapp_numbers TO taskiem_dispatch;

-- The tenant an active number belongs to, and whether it is on the
-- tenant's own app: all an inbound webhook may learn before it is routed.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_number_route(p_phone_number_id text)
RETURNS TABLE (tenant_id uuid, own_app boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT n.tenant_id, n.own_app FROM whatsapp_numbers n JOIN tenants t ON t.id = n.tenant_id AND t.status = 'active'
   WHERE n.phone_number_id = p_phone_number_id AND n.status = 'active'
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_wa_number_route(text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_wa_number_route(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_wa_number_route(text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_wa_number_route(text);
DROP TABLE whatsapp_public_sessions;
DROP TABLE whatsapp_public_workflows;
DROP TABLE whatsapp_own_contacts;
DROP TABLE whatsapp_numbers;
