-- Suppliers and purchase orders (docs/spec/stock.md §6, K3).
--
-- A supplier is a person or business outside the business (decision 0039),
-- keyed as riders are by the keyed hash of their phone, the phone sealed.
-- Their name is the one the catalogue's supplier column holds in the
-- owner's own table, so it is kept plain; the items they supply are the
-- catalogue's items naming them.
--
-- A purchase order is drafted (by a person or a workflow), approved and
-- sent in one step, under the business's limit above which a second person
-- approves; confirmed by the supplier's tap or marked confirmed by the
-- owner; received in part or in full (each receipt a stock movement naming
-- it); and paid as an expense in the money log, each payment a row here.

-- +goose Up
CREATE TABLE suppliers (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  phone_hash  bytea NOT NULL CHECK (length(phone_hash) = 32),
  phone       jsonb NOT NULL,  -- sealed E.164
  invited_at  timestamptz,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  retired_at  timestamptz
);
CREATE UNIQUE INDEX suppliers_name ON suppliers (tenant_id, lower(name)) WHERE retired_at IS NULL;
CREATE UNIQUE INDEX suppliers_phone ON suppliers (tenant_id, phone_hash) WHERE retired_at IS NULL;
ALTER TABLE suppliers ENABLE ROW LEVEL SECURITY;
ALTER TABLE suppliers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON suppliers TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON suppliers TO taskiem_app;

-- A business's stock settings: purchase orders above purchase_limit_kobo
-- need a second person's approval (NULL: the owner approves any).
CREATE TABLE stock_settings (
  tenant_id            uuid PRIMARY KEY REFERENCES tenants(id),
  purchase_limit_kobo  bigint CHECK (purchase_limit_kobo >= 0),
  updated_by           text NOT NULL,
  updated_at           timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE stock_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON stock_settings TO taskiem_app;

CREATE TABLE purchase_orders (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  reference      text NOT NULL CHECK (reference ~ '^P-[A-Z0-9]{6}$'),
  supplier_id    uuid NOT NULL REFERENCES suppliers(id),
  -- [{item_id, item, unit, quantity, cost_kobo, received}]
  lines          jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array' AND jsonb_array_length(lines) BETWEEN 1 AND 50),
  total_kobo     bigint NOT NULL CHECK (total_kobo >= 0),
  status         text NOT NULL DEFAULT 'drafted'
                   CHECK (status IN ('drafted', 'sent', 'confirmed', 'part_received', 'received', 'cancelled')),
  note           text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  drafted_by     text NOT NULL,
  approved_by    text,
  sent_at        timestamptz,
  confirmed_at   timestamptz,
  confirmed_how  text CHECK (confirmed_how IN ('supplier', 'owner')),
  -- The supplier's Change: what they can supply instead, in their words.
  counter        text CHECK (length(counter) <= 1000),
  counter_at     timestamptz,
  received_at    timestamptz,
  cancelled_at   timestamptz,
  -- Received value less what was paid is owed to the supplier.
  received_kobo  bigint NOT NULL DEFAULT 0 CHECK (received_kobo >= 0),
  paid_kobo      bigint NOT NULL DEFAULT 0 CHECK (paid_kobo >= 0),
  key            text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, reference),
  UNIQUE (tenant_id, key),
  CHECK (status = 'drafted' OR status = 'cancelled' OR (approved_by IS NOT NULL AND sent_at IS NOT NULL))
);
CREATE INDEX purchase_orders_open ON purchase_orders (tenant_id, supplier_id, created_at)
  WHERE status IN ('drafted', 'sent', 'confirmed', 'part_received');
ALTER TABLE purchase_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_orders TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON purchase_orders TO taskiem_app;

CREATE TABLE purchase_payments (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  purchase_id  uuid NOT NULL REFERENCES purchase_orders(id),
  entry_id     uuid NOT NULL UNIQUE,
  amount_kobo  bigint NOT NULL CHECK (amount_kobo > 0),
  paid_how     text NOT NULL CHECK (paid_how IN ('cash', 'transfer')),
  paid_by      text NOT NULL,
  paid_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX purchase_payments_order ON purchase_payments (tenant_id, purchase_id);
ALTER TABLE purchase_payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_payments TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON purchase_payments TO taskiem_app;

-- A supplier's words to the platform number (CONFIRM, CHANGE ...) find
-- their orders across businesses, as a rider's do: by the phone a WhatsApp
-- message comes from, or the contact a Telegram chat is linked to.
CREATE POLICY dispatch ON suppliers FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, phone_hash, retired_at) ON suppliers TO taskiem_dispatch;
CREATE POLICY dispatch ON purchase_orders FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, supplier_id, reference, status, sent_at) ON purchase_orders TO taskiem_dispatch;
-- +goose StatementBegin
-- The orders a supplier may still confirm or change (sent or confirmed,
-- of live businesses), the newest first: never their items or prices.
CREATE FUNCTION taskiem_supplier_orders(p_phone_hash bytea, p_channel text, p_address_hash bytea)
RETURNS TABLE (tenant_id uuid, purchase_id uuid, reference text, status text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.tenant_id, o.id, o.reference, o.status
    FROM purchase_orders o JOIN suppliers s ON s.id = o.supplier_id JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active'
   WHERE s.retired_at IS NULL AND o.status IN ('sent', 'confirmed')
     AND (s.phone_hash = p_phone_hash
          OR s.phone_hash IN (SELECT c.phone_hash FROM reach_channels ch JOIN reach_contacts c ON c.id = ch.contact_id
                               WHERE ch.channel = p_channel AND ch.address_hash = p_address_hash AND c.tenant_id = s.tenant_id AND ch.stopped_at IS NULL))
   ORDER BY o.sent_at DESC, o.id DESC LIMIT 10
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_supplier_orders(bytea, text, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_supplier_orders(bytea, text, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_supplier_orders(bytea, text, bytea) TO taskiem_app;

-- An order goes to its supplier through the chat outbox, naming their
-- contact and the order (run_id) and what is sent (step_id).
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening', 'supplier'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family', 'rider', 'supplier')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider', 'supplier') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind IN ('money_daily', 'sales_evening')) = (digest_id IS NOT NULL)));

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind = 'supplier';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family', 'rider')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind IN ('money_daily', 'sales_evening')) = (digest_id IS NOT NULL)));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening'));
DROP FUNCTION taskiem_supplier_orders(bytea, text, bytea);
DROP TABLE purchase_payments;
DROP TABLE purchase_orders;
DROP TABLE stock_settings;
DROP TABLE suppliers;
