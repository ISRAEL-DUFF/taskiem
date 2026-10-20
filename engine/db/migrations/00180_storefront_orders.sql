-- Storefront orders, enquiries and callbacks (Phase 5, S3;
-- docs/spec/storefronts.md 6.3 to 6.5, 8.2; docs/storefronts.md).
--
-- A submission travels: the public host checks the form and writes it,
-- encrypted, to storefront_inbox (the public role holds no key to the
-- vault, so it cannot seal personal data itself); the api role takes it
-- from the inbox, seals the customer's details into storefront_orders,
-- makes the payment link when the order is paid at once, and starts the
-- action's workflow with the storefront trigger. Inbox rows live seconds.
-- +goose Up
CREATE TABLE storefront_actions (
  storefront_id  uuid NOT NULL REFERENCES storefronts(id),
  kind           text NOT NULL CHECK (kind IN ('order', 'enquiry', 'callback')),
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  enabled        boolean NOT NULL DEFAULT false,
  label          text NOT NULL CHECK (length(label) BETWEEN 1 AND 40),
  workflow_id    uuid REFERENCES workflows(id),
  payment        text NOT NULL DEFAULT 'after_accept' CHECK (payment IN ('after_accept', 'at_order', 'none')),
  options        jsonb NOT NULL DEFAULT '{}',
  updated_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (storefront_id, kind)
);
ALTER TABLE storefront_actions ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_actions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_actions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON storefront_actions TO taskiem_app;
CREATE POLICY dispatch ON storefront_actions FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (storefront_id, kind, enabled, label, payment, options) ON storefront_actions TO taskiem_dispatch;

-- Submissions on their way from the public host. Platform data: the public
-- role writes through taskiem_public_submit only; the api role claims and
-- deletes through the functions below.
CREATE TABLE storefront_inbox (
  id             uuid PRIMARY KEY,
  storefront_id  uuid NOT NULL REFERENCES storefronts(id),
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  kind           text NOT NULL,
  reference      text NOT NULL UNIQUE,
  idem           bytea NOT NULL UNIQUE CHECK (length(idem) = 32),
  status_hash    bytea NOT NULL CHECK (length(status_hash) = 32),
  payload        bytea NOT NULL CHECK (length(payload) <= 65536),
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until  timestamptz
);
ALTER TABLE storefront_inbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_inbox FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON storefront_inbox TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON storefront_inbox TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_inbox TO taskiem_dispatch;

-- Orders (and enquiries and callbacks): the record the workflow moves.
CREATE TABLE storefront_orders (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  storefront_id   uuid NOT NULL REFERENCES storefronts(id),
  kind            text NOT NULL CHECK (kind IN ('order', 'enquiry', 'callback')),
  reference       text NOT NULL UNIQUE,
  status_hash     bytea NOT NULL,
  status          text NOT NULL CHECK (status IN ('received', 'awaiting_payment', 'paid', 'accepted', 'declined', 'expired', 'completed', 'cancelled', 'refund_due')),
  customer        jsonb NOT NULL,       -- sealed: {name, phone, email, address, message, best_time}
  subject_id      text NOT NULL,        -- the customer's personal-data subject (their phone's)
  basket          jsonb NOT NULL DEFAULT '[]',
  total_kobo      bigint NOT NULL DEFAULT 0,
  fulfilment      text CHECK (fulfilment IN ('pickup', 'delivery')),
  payment         text NOT NULL CHECK (payment IN ('after_accept', 'at_order', 'none')),
  payment_ref     text,
  payment_url     text,
  paid_at         timestamptz,
  run_id          uuid,
  note            text,
  -- Stock lowered on Accept, by catalogue row: {"<row>": {"from": 5, "to": 3, "done": true}}.
  -- Each change is recorded before it is sent, so a retry sends the same change.
  stock           jsonb,
  decided_by      text,
  decided_at      timestamptz,
  expires_at      timestamptz,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX storefront_orders_list ON storefront_orders (tenant_id, created_at DESC);
CREATE INDEX storefront_orders_payment ON storefront_orders (tenant_id, expires_at) WHERE status = 'awaiting_payment';
ALTER TABLE storefront_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_orders TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON storefront_orders TO taskiem_app;
CREATE POLICY dispatch ON storefront_orders FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, reference, status_hash, status, total_kobo, payment, payment_url, storefront_id, kind, expires_at) ON storefront_orders TO taskiem_dispatch;

-- The storefront trigger: one workflow per storefront action and
-- environment, registered when the workflow is deployed.
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd', 'storefront'));
ALTER TABLE triggers ADD COLUMN storefront_action text CHECK (storefront_action IN ('order', 'enquiry', 'callback'));

-- The public host's write: a checked submission, encrypted with the inbox
-- key. A repeated idempotency token returns the first reference. Refused
-- for a storefront that is not published and open, or an action that is
-- not enabled.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_submit(p_storefront uuid, p_kind text, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  ref text;
BEGIN
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem;
  IF ref IS NOT NULL THEN
    RETURN ref;
  END IF;
  -- A reference an order already has (its inbox row gone) is taken: the
  -- public host draws another.
  IF EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference) THEN
    RAISE EXCEPTION 'reference taken' USING ERRCODE = '23505', CONSTRAINT = 'storefront_inbox_reference_key';
  END IF;
  SELECT s.tenant_id INTO t FROM storefronts s
    JOIN tenants tn ON tn.id = s.tenant_id AND tn.status = 'active'
    JOIN storefront_actions a ON a.storefront_id = s.id AND a.kind = p_kind AND a.enabled
   WHERE s.id = p_storefront AND s.status = 'published';
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload)
    VALUES (gen_random_uuid(), p_storefront, t, p_kind, p_reference, p_idem, p_status_hash, p_payload)
    ON CONFLICT (idem) DO NOTHING;
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem;
  RETURN ref;
END
$$;
-- +goose StatementEnd

-- What the customer's status page shows, for the reference and its key:
-- nothing personal.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_order_status(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, kind text, total_kobo bigint, payment text, payment_url text, storefront_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.status, o.kind, o.total_kobo, o.payment, o.payment_url, o.storefront_id FROM storefront_orders o
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash
  UNION ALL
  SELECT 'processing', i.kind, 0, '', NULL, i.storefront_id FROM storefront_inbox i
   WHERE i.reference = p_reference AND i.status_hash = p_status_hash
     AND NOT EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference)
$$;
-- +goose StatementEnd

-- The api role's side of the inbox.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_inbox_claim(p_limit int)
RETURNS TABLE (id uuid, storefront_id uuid, tenant_id uuid, kind text, reference text, status_hash bytea, payload bytea, created_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE storefront_inbox i SET claimed_until = now() + interval '2 minutes'
   WHERE i.id IN (SELECT x.id FROM storefront_inbox x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING i.id, i.storefront_id, i.tenant_id, i.kind, i.reference, i.status_hash, i.payload, i.created_at
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_inbox_done(p_id uuid)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM storefront_inbox WHERE id = p_id
$$;
-- +goose StatementEnd

-- Tenants with orders waiting for payment, for the payment checker.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_payment_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT o.tenant_id FROM storefront_orders o WHERE o.status = 'awaiting_payment'
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_public_submit(uuid, text, text, bytea, bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_order_status(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_storefront_inbox_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_storefront_inbox_done(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_storefront_payment_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_submit(uuid, text, text, bytea, bytea, bytea), taskiem_public_order_status(text, bytea),
  taskiem_storefront_inbox_claim(int), taskiem_storefront_inbox_done(uuid), taskiem_storefront_payment_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_submit(uuid, text, text, bytea, bytea, bytea), taskiem_public_order_status(text, bytea) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_storefront_inbox_claim(int), taskiem_storefront_inbox_done(uuid), taskiem_storefront_payment_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_storefront_payment_tenants();
DROP FUNCTION taskiem_storefront_inbox_done(uuid);
DROP FUNCTION taskiem_storefront_inbox_claim(int);
DROP FUNCTION taskiem_public_order_status(text, bytea);
DROP FUNCTION taskiem_public_submit(uuid, text, text, bytea, bytea, bytea);
DELETE FROM triggers WHERE type = 'storefront';
ALTER TABLE triggers DROP COLUMN storefront_action;
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd'));
DROP TABLE storefront_orders;
DROP TABLE storefront_inbox;
DROP TABLE storefront_actions;
