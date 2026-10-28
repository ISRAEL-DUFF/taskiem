-- Dispatch (docs/spec/dispatch.md D2 to D5, decision 0055): an order
-- offered to a business's own rider, the delivery the rider drives with
-- their taps, payment at the door, cash and what each rider owes, their
-- fees and payouts, and the customer's rating.
--
-- A delivery is one order offered to one rider: offered, then accepted
-- (the rider holds the order) or declined or passed (the offer window
-- ran out), then picked up, arrived and delivered, or released by the
-- owner. Only one rider holds an order, and only one offer for it is out
-- at a time. Riders are reached as in 00238, on the outbox's new 'rider'
-- kind; its buttons are signed for the business, the delivery and the
-- rider's contact.

-- +goose Up
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_status_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_status_check CHECK (status IN ('received', 'awaiting_payment', 'paid', 'accepted',
  'declined', 'expired', 'completed', 'cancelled', 'refund_due', 'no_show', 'price_changed', 'refunded', 'dispatched', 'on_the_way', 'arrived'));
-- Pay on delivery: paid at the door, by a one-time account number issued
-- when the rider arrives, or in cash.
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_payment_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_payment_check CHECK (payment IN ('after_accept', 'at_order', 'none', 'on_delivery'));
-- Where the order goes, beyond the area: kept sealed with the customer
-- (landmark, pin); a business with no areas takes the area as typed in
-- zone, with no fee.

-- How a business dispatches: offering by rotation or asking the owner,
-- the offer window, when a quiet rider is raised, whether proof is asked,
-- the cash a rider may owe before it is a leak, and the rating below
-- which a rider is raised.
CREATE TABLE dispatch_settings (
  tenant_id            uuid PRIMARY KEY REFERENCES tenants(id),
  mode                 text NOT NULL DEFAULT 'rotation' CHECK (mode IN ('rotation', 'ask')),
  offer_minutes        int NOT NULL DEFAULT 5 CHECK (offer_minutes BETWEEN 1 AND 60),
  pickup_minutes       int NOT NULL DEFAULT 20 CHECK (pickup_minutes BETWEEN 5 AND 240),
  arrive_minutes       int NOT NULL DEFAULT 90 CHECK (arrive_minutes BETWEEN 10 AND 600),
  proof                boolean NOT NULL DEFAULT false,
  owe_limit_kobo       bigint NOT NULL DEFAULT 2000000 CHECK (owe_limit_kobo >= 0),
  rating_mark          numeric(2,1) NOT NULL DEFAULT 3.0 CHECK (rating_mark BETWEEN 1 AND 5),
  -- The pay-riders workflow Riders > Pay made (from pay-riders-weekly).
  payout_workflow_id   uuid REFERENCES workflows(id),
  updated_at           timestamptz NOT NULL DEFAULT now()
);

-- Not answering, per business: three offers unanswered in a day mark a
-- rider until they tap anything. A rider's bank account for payouts,
-- sealed, with what may be shown.
ALTER TABLE riders
  ADD COLUMN missed int NOT NULL DEFAULT 0,
  ADD COLUMN missed_on date,
  ADD COLUMN not_answering_since timestamptz,
  ADD COLUMN bank jsonb,
  ADD COLUMN bank_shown text CHECK (length(bank_shown) <= 120);

CREATE TABLE deliveries (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  order_id       uuid NOT NULL REFERENCES storefront_orders(id),
  rider_id       uuid NOT NULL REFERENCES riders(id),
  status         text NOT NULL CHECK (status IN ('offered', 'declined', 'passed', 'withdrawn', 'accepted', 'picked_up', 'arrived', 'delivered', 'released')),
  offered_by     text NOT NULL,
  offered_at     timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  answered_at    timestamptz,
  accepted_at    timestamptz,
  picked_up_at   timestamptz,
  arrived_at     timestamptz,
  delivered_at   timestamptz,
  released_at    timestamptz,
  fee_kobo       bigint NOT NULL DEFAULT 0 CHECK (fee_kobo >= 0),
  -- How it was paid at the door: transfer (the one-time number) or cash.
  paid_how       text CHECK (paid_how IN ('transfer', 'cash')),
  -- Proof of delivery: the photo in the business's own storage, and a
  -- location the rider sent at arrival (a maps link).
  proof          text CHECK (length(proof) <= 300),
  location       text CHECK (length(location) <= 300),
  rating         smallint CHECK (rating BETWEEN 1 AND 5),
  rated_at       timestamptz,
  payout_id      uuid,
  created_at     timestamptz NOT NULL DEFAULT now()
);
-- Exactly one rider holds an order; one offer is out at a time.
CREATE UNIQUE INDEX deliveries_held ON deliveries (order_id) WHERE status IN ('accepted', 'picked_up', 'arrived', 'delivered');
CREATE UNIQUE INDEX deliveries_offered ON deliveries (order_id) WHERE status = 'offered';
CREATE INDEX deliveries_rider ON deliveries (rider_id, offered_at DESC);
CREATE INDEX deliveries_tenant ON deliveries (tenant_id, offered_at DESC);
CREATE INDEX deliveries_open ON deliveries (expires_at) WHERE status IN ('offered', 'accepted', 'picked_up');

-- Cash a rider took at the door and owes the business, and what was
-- remitted against it, oldest first.
CREATE TABLE rider_remittances (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  rider_id      uuid NOT NULL REFERENCES riders(id),
  delivery_id   uuid NOT NULL UNIQUE REFERENCES deliveries(id),
  amount_kobo   bigint NOT NULL CHECK (amount_kobo > 0),
  paid_kobo     bigint NOT NULL DEFAULT 0 CHECK (paid_kobo >= 0),
  created_at    timestamptz NOT NULL DEFAULT now(),
  settled_at    timestamptz,
  CHECK (paid_kobo <= amount_kobo)
);
CREATE INDEX rider_remittances_open ON rider_remittances (rider_id, created_at) WHERE settled_at IS NULL;
CREATE TABLE rider_remittance_parts (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  remittance_id  uuid NOT NULL REFERENCES rider_remittances(id),
  -- What paid it: a money log entry (a transfer, or cash handed in), or
  -- a payout it was netted from.
  entry_id       uuid REFERENCES money_entries(id),
  payout_id      uuid,
  how            text NOT NULL CHECK (how IN ('transfer', 'cash', 'netted')),
  amount_kobo    bigint NOT NULL CHECK (amount_kobo > 0),
  by             text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  CHECK ((entry_id IS NULL) <> (payout_id IS NULL))
);

-- A rider's payout: their fees for deliveries since the last, less the
-- cash they owe, through the pay-riders workflow under approval.
CREATE TABLE rider_payouts (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  batch_id      uuid NOT NULL,
  rider_id      uuid NOT NULL REFERENCES riders(id),
  deliveries    int NOT NULL CHECK (deliveries >= 0),
  fees_kobo     bigint NOT NULL CHECK (fees_kobo >= 0),
  owed_kobo     bigint NOT NULL CHECK (owed_kobo >= 0),
  net_kobo      bigint NOT NULL CHECK (net_kobo >= 0),
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed', 'cancelled')),
  run_id        uuid,
  created_at    timestamptz NOT NULL DEFAULT now(),
  settled_at    timestamptz,
  UNIQUE (batch_id, rider_id)
);
CREATE INDEX rider_payouts_tenant ON rider_payouts (tenant_id, created_at DESC);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['dispatch_settings', 'deliveries', 'rider_remittances', 'rider_remittance_parts', 'rider_payouts'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd
-- The dispatch role reads only what its definers need: a delivery's
-- state, times and rating, and whether a business has cash owed. Never
-- settings, payouts or remittance amounts.
CREATE POLICY dispatch ON deliveries TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (id, tenant_id, order_id, rider_id, status, offered_at, accepted_at, picked_up_at, arrived_at, delivered_at, rated_at, rating)
  ON deliveries TO taskiem_dispatch;
CREATE POLICY dispatch ON rider_remittances FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, settled_at) ON rider_remittances TO taskiem_dispatch;

-- What a business may know of its riders' presence, now with their open
-- deliveries anywhere: busy (with how many and since when) while they
-- hold one, whatever business it is for; what they said otherwise. Never
-- a business or an order.
DROP FUNCTION taskiem_rider_presence(bytea[]);
-- +goose StatementBegin
CREATE FUNCTION taskiem_rider_presence(p_hashes bytea[])
RETURNS TABLE (phone_hash bytea, said text, until timestamptz, said_at timestamptz, open int, open_since timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH mine AS (
    SELECT DISTINCT r.phone_hash FROM riders r WHERE r.phone_hash = ANY (p_hashes) AND r.tenant_id = ANY (taskiem_tenant_scope())
  ), held AS (
    SELECT r.phone_hash, count(*)::int AS n, min(d.accepted_at) AS since
      FROM deliveries d JOIN riders r ON r.id = d.rider_id
     WHERE r.phone_hash IN (SELECT phone_hash FROM mine) AND d.status IN ('accepted', 'picked_up', 'arrived')
     GROUP BY r.phone_hash
  )
  SELECT m.phone_hash, p.said, p.until, p.said_at, coalesce(h.n, 0), h.since
    FROM mine m LEFT JOIN rider_presence p ON p.phone_hash = m.phone_hash AND (p.said = 'off' OR p.until > now())
    LEFT JOIN held h ON h.phone_hash = m.phone_hash
   WHERE p.phone_hash IS NOT NULL OR h.phone_hash IS NOT NULL
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The deliveries a rider holds or is offered now, whichever business they
-- ride for, for their words (picked up, arrived, delivered) and photos:
-- the rider is the phone given (WhatsApp) or the contact the chat address
-- is linked to (Telegram).
CREATE FUNCTION taskiem_rider_deliveries(p_phone_hash bytea, p_channel text, p_address_hash bytea)
RETURNS TABLE (tenant_id uuid, delivery_id uuid, status text, changed_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.tenant_id, d.id, d.status, greatest(d.offered_at, d.accepted_at, d.picked_up_at, d.arrived_at, d.delivered_at)
    FROM deliveries d JOIN riders r ON r.id = d.rider_id JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active'
   WHERE r.retired_at IS NULL
     AND (r.phone_hash = p_phone_hash
          OR r.phone_hash IN (SELECT c.phone_hash FROM reach_channels ch JOIN reach_contacts c ON c.id = ch.contact_id
                               WHERE ch.channel = p_channel AND ch.address_hash = p_address_hash))
     AND (d.status IN ('offered', 'accepted', 'picked_up', 'arrived')
          OR (d.status = 'delivered' AND d.delivered_at > now() - interval '2 hours'))
   ORDER BY 4 DESC
$$;
-- +goose StatementEnd
-- +goose StatementBegin
-- The businesses with deliveries out, for the sweep (offers that ran out,
-- quiet riders), with ratings to watch, or with cash riders owe.
CREATE FUNCTION taskiem_dispatch_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.tenant_id FROM deliveries d JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active'
   WHERE d.status IN ('offered', 'accepted', 'picked_up', 'arrived') OR d.rated_at > now() - interval '1 day'
  UNION
  SELECT r.tenant_id FROM rider_remittances r JOIN tenants t ON t.id = r.tenant_id AND t.status = 'active' WHERE r.settled_at IS NULL
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_rider_presence(bytea[]) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_rider_deliveries(bytea, text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_dispatch_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_rider_presence(bytea[]), taskiem_rider_deliveries(bytea, text, bytea), taskiem_dispatch_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_rider_presence(bytea[]), taskiem_rider_deliveries(bytea, text, bytea), taskiem_dispatch_tenants() TO taskiem_app;

-- +goose StatementBegin
-- The customer rates a delivered order from its status page, once: false
-- when there is nothing to rate (no delivery, not delivered, rated).
CREATE FUNCTION taskiem_public_order_rate(p_reference text, p_status_hash bytea, p_rating int)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  did uuid;
BEGIN
  IF p_rating NOT BETWEEN 1 AND 5 THEN
    RETURN false;
  END IF;
  SELECT d.id INTO did FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash AND d.status = 'delivered' AND d.rating IS NULL
     AND d.delivered_at > now() - interval '14 days'
   FOR UPDATE OF d;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  UPDATE deliveries SET rating = p_rating, rated_at = now() WHERE id = did;
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- What an order's status page shows of its delivery: its state, and
-- whether it can still be rated (or the rating given). Never the rider.
CREATE FUNCTION taskiem_public_order_delivery(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, rating smallint, can_rate boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.status, d.rating, d.status = 'delivered' AND d.rating IS NULL AND d.delivered_at > now() - interval '14 days'
    FROM deliveries d JOIN storefront_orders o ON o.id = d.order_id
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash AND d.status IN ('accepted', 'picked_up', 'arrived', 'delivered')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_order_rate(text, bytea, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_order_delivery(text, bytea) OWNER TO taskiem_dispatch;
GRANT UPDATE (rating, rated_at) ON deliveries TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_order_rate(text, bytea, int), taskiem_public_order_delivery(text, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_order_rate(text, bytea, int), taskiem_public_order_delivery(text, bytea) TO taskiem_public;

-- A rider's offers and taps on their chat app: the outbox's 'rider' kind,
-- to their contact, the delivery in run_id and what to send in step_id.
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match', 'money_daily', 'school_approval', 'school_family', 'rider'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family', 'rider')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind = 'money_daily') = (digest_id IS NOT NULL)));

-- Cash a rider owes beyond the business's limit is a leak.
ALTER TABLE money_leaks DROP CONSTRAINT money_leaks_kind_check;
ALTER TABLE money_leaks ADD CONSTRAINT money_leaks_kind_check CHECK (kind IN ('unmatched', 'overdue', 'duplicate', 'overpaid', 'void_paid', 'fee', 'rider_owes'));

-- +goose Down
ALTER TABLE money_leaks DROP CONSTRAINT money_leaks_kind_check;
DELETE FROM money_leaks WHERE kind = 'rider_owes';
ALTER TABLE money_leaks ADD CONSTRAINT money_leaks_kind_check CHECK (kind IN ('unmatched', 'overdue', 'duplicate', 'overpaid', 'void_paid', 'fee'));
DELETE FROM whatsapp_outbox WHERE kind = 'rider';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind = 'money_daily') = (digest_id IS NOT NULL)));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match', 'money_daily', 'school_approval', 'school_family'));
DROP FUNCTION taskiem_public_order_delivery(text, bytea);
DROP FUNCTION taskiem_public_order_rate(text, bytea, int);
DROP FUNCTION taskiem_dispatch_tenants();
DROP FUNCTION taskiem_rider_deliveries(bytea, text, bytea);
DROP FUNCTION taskiem_rider_presence(bytea[]);
-- +goose StatementBegin
CREATE FUNCTION taskiem_rider_presence(p_hashes bytea[])
RETURNS TABLE (phone_hash bytea, said text, until timestamptz, said_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.phone_hash, p.said, p.until, p.said_at FROM rider_presence p
   WHERE p.phone_hash = ANY (p_hashes) AND (p.said = 'off' OR p.until > now())
     AND EXISTS (SELECT 1 FROM riders r WHERE r.phone_hash = p.phone_hash AND r.tenant_id = ANY (taskiem_tenant_scope()))
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_rider_presence(bytea[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_rider_presence(bytea[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_rider_presence(bytea[]) TO taskiem_app;
DROP TABLE rider_payouts;
DROP TABLE rider_remittance_parts;
DROP TABLE rider_remittances;
DROP TABLE deliveries;
ALTER TABLE riders DROP COLUMN bank_shown, DROP COLUMN bank, DROP COLUMN not_answering_since, DROP COLUMN missed_on, DROP COLUMN missed;
DROP TABLE dispatch_settings;
UPDATE storefront_orders SET payment = 'none' WHERE payment = 'on_delivery';
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_payment_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_payment_check CHECK (payment IN ('after_accept', 'at_order', 'none'));
UPDATE storefront_orders SET status = 'accepted' WHERE status IN ('dispatched', 'on_the_way', 'arrived');
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_status_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_status_check CHECK (status IN ('received', 'awaiting_payment', 'paid', 'accepted',
  'declined', 'expired', 'completed', 'cancelled', 'refund_due', 'no_show', 'price_changed', 'refunded'));
