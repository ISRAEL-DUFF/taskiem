-- The WhatsApp template registry (docs/whatsapp.md#templates). Outside a
-- person's 24-hour window WhatsApp delivers only a template Meta has
-- approved, in the WhatsApp Business Account (WABA) the number belongs
-- to: the platform number's (TASKIEM_WHATSAPP_WABA_ID) or an own
-- number's (whatsapp_numbers.waba_id). One row per account, template name
-- and language says what Meta knows of it: not submitted, pending,
-- approved, rejected (with the reason), paused or disabled, with Meta's
-- id for it, when Taskiem submitted it and when its status was last
-- checked (the status webhook, a poll). Nothing is sent outside the
-- window on a template not held here as approved.
--
-- Rows of the platform's account have no tenant and are visible in every
-- scope (statuses are not a secret); an own number's rows are its
-- tenant's data under row-level security.

-- +goose Up

-- A family message to a guardian on WhatsApp or Telegram (kind
-- school_family, migration 00216) names the guardian's contact and no
-- member, which the outbox's check (00205) did not allow: such a message
-- could not be queued, so guardians who tapped in for the school's
-- updates never got its messages there. Allowed now, as an invoice to a
-- contact is.
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind = 'money_daily') = (digest_id IS NOT NULL)));

CREATE TABLE whatsapp_templates (
  waba_id       text NOT NULL CHECK (waba_id ~ '^[0-9]{5,32}$'),
  name          text NOT NULL CHECK (name ~ '^[a-z0-9_]+$' AND length(name) <= 512),
  language      text NOT NULL CHECK (length(language) BETWEEN 2 AND 16),
  tenant_id     uuid REFERENCES tenants(id) ON DELETE CASCADE,
  meta_id       text,
  status        text NOT NULL DEFAULT 'not_submitted'
                CHECK (status IN ('not_submitted', 'pending', 'approved', 'rejected', 'paused', 'disabled')),
  reason        text NOT NULL DEFAULT '',
  submitted_at  timestamptz,
  checked_at    timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (waba_id, name, language)
);
CREATE INDEX whatsapp_templates_tenant ON whatsapp_templates (tenant_id) WHERE tenant_id IS NOT NULL;
ALTER TABLE whatsapp_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_templates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON whatsapp_templates TO taskiem_app
  USING (tenant_id IS NULL OR tenant_id = ANY (taskiem_tenant_scope()))
  WITH CHECK (tenant_id IS NULL OR tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON whatsapp_templates TO taskiem_app;

-- The active own numbers, for the hourly status poll: all the poller may
-- learn before it works in each tenant's scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_own_numbers()
RETURNS TABLE (tenant_id uuid, phone_number_id text, waba_id text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT n.tenant_id, n.phone_number_id, n.waba_id FROM whatsapp_numbers n JOIN tenants t ON t.id = n.tenant_id AND t.status = 'active'
   WHERE n.status = 'active' ORDER BY n.created_at
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_wa_own_numbers() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_wa_own_numbers() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_wa_own_numbers() TO taskiem_app;

-- The tenant whose active own number is on a WhatsApp Business Account:
-- how a template status webhook, which names only the account, finds
-- whose registry to write.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_waba_tenant(p_waba_id text)
RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT n.tenant_id FROM whatsapp_numbers n JOIN tenants t ON t.id = n.tenant_id AND t.status = 'active'
   WHERE n.waba_id = p_waba_id AND n.status = 'active' ORDER BY n.created_at LIMIT 1
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_wa_waba_tenant(text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_wa_waba_tenant(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_wa_waba_tenant(text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_wa_waba_tenant(text);
DROP FUNCTION taskiem_wa_own_numbers();
DROP TABLE whatsapp_templates;
DELETE FROM whatsapp_outbox WHERE kind = 'school_family' AND user_id IS NULL;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind <> 'invoice' OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind = 'money_daily') = (digest_id IS NOT NULL)));
