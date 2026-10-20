-- +goose Up
-- Storefront bookings (docs/spec/storefronts.md 6.9) and the price check
-- before a run (8.1): services from a data table with their copy, the
-- business's availability, slots held so no two customers take one,
-- bookings as storefront_orders of kind booking, and the changes customers
-- make from their status link, queued for the api role.

ALTER TABLE storefronts
  ADD COLUMN services_table   uuid REFERENCES data_tables(id) ON DELETE SET NULL,
  ADD COLUMN services_columns jsonb NOT NULL DEFAULT '{}',
  ADD COLUMN availability     jsonb NOT NULL DEFAULT '{}',
  -- The Lagos date the owner was last sent the day's bookings.
  ADD COLUMN bookings_sent_on date;
GRANT SELECT (availability, services_table) ON storefronts TO taskiem_dispatch;

-- The services' public columns, as storefront_catalogue holds the goods'.
CREATE TABLE storefront_services (
  storefront_id  uuid NOT NULL REFERENCES storefronts(id) ON DELETE CASCADE,
  row_id         text NOT NULL,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  position       int NOT NULL,
  item           jsonb NOT NULL,
  PRIMARY KEY (storefront_id, row_id)
);
ALTER TABLE storefront_services ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_services FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_services TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_services TO taskiem_app;
CREATE POLICY dispatch ON storefront_services FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (storefront_id, position, item) ON storefront_services TO taskiem_dispatch;

ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_kind_check;
ALTER TABLE storefront_actions ADD CONSTRAINT storefront_actions_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking'));
ALTER TABLE triggers DROP CONSTRAINT triggers_storefront_action_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_storefront_action_check CHECK (storefront_action IN ('order', 'enquiry', 'callback', 'booking'));

-- A booking is an order of kind booking with its service and time;
-- version counts reschedules (each one starts a new run). price_changed
-- holds an order whose prices moved in the table before its run started,
-- until the customer confirms the new ones in price_change.
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback', 'booking'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_status_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_status_check CHECK (status IN ('received', 'awaiting_payment', 'paid', 'accepted',
  'declined', 'expired', 'completed', 'cancelled', 'refund_due', 'no_show', 'price_changed'));
ALTER TABLE storefront_orders
  ADD COLUMN service_id    text,
  ADD COLUMN service_name  text,
  ADD COLUMN starts_at     timestamptz,
  ADD COLUMN ends_at       timestamptz,
  ADD COLUMN version       int NOT NULL DEFAULT 0,
  ADD COLUMN price_change  jsonb,
  -- What is paid online when less than the total (a booking's deposit).
  ADD COLUMN deposit_kobo  bigint CHECK (deposit_kobo > 0),
  -- The customer's status key, sealed under their subject, for the {link}
  -- in messages to them; only its hash (status_hash) is stored in the clear.
  ADD COLUMN status_key    jsonb,
  ADD CONSTRAINT storefront_orders_booking_time CHECK (kind <> 'booking' OR (starts_at IS NOT NULL AND ends_at > starts_at));
CREATE INDEX storefront_orders_bookings ON storefront_orders (tenant_id, starts_at) WHERE kind = 'booking';
GRANT SELECT (service_id, service_name, starts_at, ends_at, version, price_change, deposit_kobo, id, created_at) ON storefront_orders TO taskiem_dispatch;
GRANT UPDATE (status, starts_at, ends_at, version, updated_at) ON storefront_orders TO taskiem_dispatch;
CREATE POLICY dispatch_change ON storefront_orders FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);

-- Time held on the calendar: by a customer for HoldMinutes while their
-- booking is recorded, while they pay, or (expires_at NULL) by a booking.
-- until_at is the end plus the service's buffer. Every change is made
-- under the storefront's advisory lock, through taskiem_storefront_slot_free.
CREATE TABLE storefront_holds (
  reference      text PRIMARY KEY,
  storefront_id  uuid NOT NULL REFERENCES storefronts(id) ON DELETE CASCADE,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  service_id     text NOT NULL,
  starts_at      timestamptz NOT NULL,
  until_at       timestamptz NOT NULL CHECK (until_at > starts_at),
  expires_at     timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX storefront_holds_time ON storefront_holds (storefront_id, starts_at);
ALTER TABLE storefront_holds ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_holds FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_holds TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_holds TO taskiem_app;
CREATE POLICY dispatch ON storefront_holds TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_holds TO taskiem_dispatch;

-- Changes customers make from their status link (cancel, move, accept or
-- refuse new prices), applied at once to the order and its hold, and
-- queued for the api role to move the run on and tell the owner.
CREATE TABLE storefront_changes (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  order_id       uuid NOT NULL REFERENCES storefront_orders(id) ON DELETE CASCADE,
  action         text NOT NULL CHECK (action IN ('cancel', 'move', 'price_accept', 'price_decline')),
  by_whom        text NOT NULL CHECK (by_whom IN ('customer', 'owner')),
  detail         jsonb NOT NULL DEFAULT '{}',
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until  timestamptz
);
CREATE INDEX storefront_changes_queue ON storefront_changes (created_at);
ALTER TABLE storefront_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_changes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_changes TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON storefront_changes TO taskiem_app;
CREATE POLICY dispatch ON storefront_changes TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_changes TO taskiem_dispatch;

-- Whether a booking of p_service from p_start (keeping the calendar until
-- p_until) fits beside the live holds other than p_except's: nothing
-- overlapping but holds of the same service at the same start, fewer than
-- p_capacity. engine/storefront/slots.go Free is the same rule. Callers
-- hold the storefront's lock (taskiem_storefront_lock).
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_slot_free(p_storefront uuid, p_except text, p_service text, p_start timestamptz, p_until timestamptz, p_capacity int)
RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  other int;
  same int;
BEGIN
  SELECT count(*) FILTER (WHERE h.service_id <> p_service OR h.starts_at <> p_start),
         count(*) FILTER (WHERE h.service_id = p_service AND h.starts_at = p_start)
    INTO other, same
    FROM storefront_holds h
   WHERE h.storefront_id = p_storefront AND h.reference IS DISTINCT FROM p_except
     AND (h.expires_at IS NULL OR h.expires_at > now())
     AND h.starts_at < p_until AND p_start < h.until_at;
  RETURN other = 0 AND same < greatest(p_capacity, 1);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_lock(p_storefront uuid)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT pg_advisory_xact_lock(hashtextextended('storefront-calendar:' || p_storefront::text, 0))
$$;
-- +goose StatementEnd

-- The public host books: holds the slot and writes the submission in one
-- transaction. NULL when the storefront or its booking form is not open;
-- raises 23P01 (constraint storefront_holds_slot) when the slot is taken,
-- and 23505 like taskiem_public_submit when the reference is.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_book(p_storefront uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
  p_service text, p_start timestamptz, p_until timestamptz, p_capacity int)
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
  IF EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference) THEN
    RAISE EXCEPTION 'reference taken' USING ERRCODE = '23505', CONSTRAINT = 'storefront_inbox_reference_key';
  END IF;
  SELECT s.tenant_id INTO t FROM storefronts s
    JOIN tenants tn ON tn.id = s.tenant_id AND tn.status = 'active'
    JOIN storefront_actions a ON a.storefront_id = s.id AND a.kind = 'booking' AND a.enabled
   WHERE s.id = p_storefront AND s.status = 'published';
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  IF p_until <= p_start OR p_start < now() THEN
    RAISE EXCEPTION 'slot taken' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_holds_slot';
  END IF;
  PERFORM taskiem_storefront_lock(p_storefront);
  IF NOT taskiem_storefront_slot_free(p_storefront, NULL, p_service, p_start, p_until, p_capacity) THEN
    RAISE EXCEPTION 'slot taken' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_holds_slot';
  END IF;
  INSERT INTO storefront_holds (reference, storefront_id, tenant_id, service_id, starts_at, until_at, expires_at)
    VALUES (p_reference, p_storefront, t, p_service, p_start, p_until, now() + interval '10 minutes');
  INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload)
    VALUES (gen_random_uuid(), p_storefront, t, 'booking', p_reference, p_idem, p_status_hash, p_payload);
  RETURN p_reference;
END
$$;
-- +goose StatementEnd

-- What is taken on a storefront's calendar between two times, for showing
-- free slots: no references, no customers.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_busy(p_storefront uuid, p_from timestamptz, p_to timestamptz)
RETURNS TABLE (service_id text, starts_at timestamptz, until_at timestamptz, n int)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT h.service_id, h.starts_at, h.until_at, count(*)::int FROM storefront_holds h
    JOIN storefronts s ON s.id = h.storefront_id AND s.status = 'published'
   WHERE h.storefront_id = p_storefront AND (h.expires_at IS NULL OR h.expires_at > now())
     AND h.starts_at < p_to AND p_from < h.until_at
   GROUP BY h.service_id, h.starts_at, h.until_at
$$;
-- +goose StatementEnd

-- A storefront's services and availability, for its booking pages.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_storefront_services(p_storefront uuid)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('availability', s.availability,
           'services', (SELECT COALESCE(jsonb_agg(v.item ORDER BY v.position), '[]') FROM storefront_services v WHERE v.storefront_id = s.id))
    FROM storefronts s WHERE s.id = p_storefront AND s.status IN ('published', 'closed')
$$;
-- +goose StatementEnd

-- The booking behind a status link, for the status and change pages.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_booking(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, service_id text, service_name text, starts_at timestamptz, ends_at timestamptz, total_kobo bigint,
  payment text, payment_url text, storefront_id uuid, deposit_kobo bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.status, o.service_id, o.service_name, o.starts_at, o.ends_at, o.total_kobo, o.payment, o.payment_url, o.storefront_id, o.deposit_kobo
    FROM storefront_orders o
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash AND o.kind = 'booking'
$$;
-- +goose StatementEnd

-- The new total of an order or booking waiting on its customer to confirm
-- changed prices (price_changed), for its status page: what is gone by
-- name, nothing personal.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_price_change(p_reference text, p_status_hash bytea)
RETURNS TABLE (total_kobo bigint, deposit_kobo bigint, gone text[])
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE((o.price_change->>'total_kobo')::bigint, 0), COALESCE((o.price_change->>'deposit_kobo')::bigint, 0),
         ARRAY(SELECT jsonb_array_elements_text(COALESCE(o.price_change->'gone', '[]')))
    FROM storefront_orders o
   WHERE o.reference = p_reference AND o.status_hash = p_status_hash AND o.status = 'price_changed' AND o.price_change IS NOT NULL
$$;
-- +goose StatementEnd

-- A customer's change from their status link. Returns 'ok', 'too_late'
-- (past the cut-off), 'taken' (the new slot is not free) or 'not_allowed'
-- (no such booking or order, or its status takes no such change). A move
-- passes the new start, end and until (end plus buffer) and the service's
-- capacity, which the public host reads from the copy.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_change(p_reference text, p_status_hash bytea, p_action text, p_cutoff_minutes int,
  p_start timestamptz, p_end timestamptz, p_until timestamptz, p_capacity int)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  o record;
BEGIN
  SELECT x.id, x.tenant_id, x.storefront_id, x.kind, x.status, x.service_id, x.starts_at, x.ends_at, x.price_change INTO o
    FROM storefront_orders x WHERE x.reference = p_reference AND x.status_hash = p_status_hash FOR UPDATE;
  IF o.id IS NULL THEN
    RETURN 'not_allowed';
  END IF;
  IF p_action IN ('price_accept', 'price_decline') THEN
    IF o.status <> 'price_changed' THEN
      RETURN 'not_allowed';
    END IF;
    UPDATE storefront_orders SET status = CASE WHEN p_action = 'price_accept' THEN 'received' ELSE 'cancelled' END, updated_at = now()
     WHERE id = o.id;
    IF p_action = 'price_decline' THEN
      DELETE FROM storefront_holds WHERE reference = p_reference;
    END IF;
  ELSIF p_action IN ('cancel', 'move') THEN
    IF o.kind <> 'booking' OR o.status NOT IN ('received', 'accepted', 'awaiting_payment', 'paid') THEN
      RETURN 'not_allowed';
    END IF;
    IF now() > o.starts_at - make_interval(mins => greatest(p_cutoff_minutes, 0)) THEN
      RETURN 'too_late';
    END IF;
    IF p_action = 'cancel' THEN
      UPDATE storefront_orders SET status = CASE WHEN status = 'paid' THEN 'refund_due' ELSE 'cancelled' END, updated_at = now() WHERE id = o.id;
      DELETE FROM storefront_holds WHERE reference = p_reference;
    ELSE
      IF p_start IS NULL OR p_end <= p_start OR p_until < p_end OR p_start < now() THEN
        RETURN 'taken';
      END IF;
      PERFORM taskiem_storefront_lock(o.storefront_id);
      IF NOT taskiem_storefront_slot_free(o.storefront_id, p_reference, o.service_id, p_start, p_until, p_capacity) THEN
        RETURN 'taken';
      END IF;
      UPDATE storefront_holds SET starts_at = p_start, until_at = p_until WHERE reference = p_reference;
      UPDATE storefront_orders SET starts_at = p_start, ends_at = p_end, version = version + 1, updated_at = now() WHERE id = o.id;
    END IF;
  ELSE
    RETURN 'not_allowed';
  END IF;
  INSERT INTO storefront_changes (id, tenant_id, order_id, action, by_whom, detail)
    VALUES (gen_random_uuid(), o.tenant_id, o.id, p_action, 'customer',
      jsonb_build_object('from', o.starts_at, 'to', p_start, 'status', o.status));
  RETURN 'ok';
END
$$;
-- +goose StatementEnd

-- The api role's side of the change queue.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_changes_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, order_id uuid, action text, by_whom text, detail jsonb)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE storefront_changes c SET claimed_until = now() + interval '2 minutes'
   WHERE c.id IN (SELECT x.id FROM storefront_changes x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING c.id, c.tenant_id, c.order_id, c.action, c.by_whom, c.detail
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_change_done(p_id uuid)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM storefront_changes WHERE id = p_id
$$;
-- +goose StatementEnd

-- Tenants taking bookings, for the sweeper (expiry, the day's list).
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_booking_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM storefronts s
    JOIN storefront_actions a ON a.storefront_id = s.id AND a.kind = 'booking' AND a.enabled
   WHERE s.status IN ('published', 'closed')
  UNION
  SELECT DISTINCT o.tenant_id FROM storefront_orders o WHERE o.status IN ('price_changed', 'received') AND o.created_at > now() - interval '30 days'
$$;
-- +goose StatementEnd

-- Holds long past, which offer nothing.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_holds_sweep()
RETURNS int
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH gone AS (DELETE FROM storefront_holds WHERE until_at < now() - interval '1 day' OR expires_at < now() - interval '1 day' RETURNING 1)
  SELECT count(*)::int FROM gone
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_storefront_slot_free(uuid, text, text, timestamptz, timestamptz, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_storefront_lock(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_book(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_busy(uuid, timestamptz, timestamptz) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_storefront_services(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_booking(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_price_change(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_change(text, bytea, text, int, timestamptz, timestamptz, timestamptz, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_storefront_changes_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_storefront_change_done(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_storefront_booking_tenants() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_storefront_holds_sweep() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefront_slot_free(uuid, text, text, timestamptz, timestamptz, int), taskiem_storefront_lock(uuid),
  taskiem_public_book(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int), taskiem_public_busy(uuid, timestamptz, timestamptz),
  taskiem_public_storefront_services(uuid), taskiem_public_booking(text, bytea), taskiem_public_price_change(text, bytea),
  taskiem_public_change(text, bytea, text, int, timestamptz, timestamptz, timestamptz, int), taskiem_storefront_changes_claim(int), taskiem_storefront_change_done(uuid), taskiem_storefront_booking_tenants(),
  taskiem_storefront_holds_sweep() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_book(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int), taskiem_public_busy(uuid, timestamptz, timestamptz),
  taskiem_public_storefront_services(uuid), taskiem_public_booking(text, bytea), taskiem_public_price_change(text, bytea),
  taskiem_public_change(text, bytea, text, int, timestamptz, timestamptz, timestamptz, int) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_storefront_slot_free(uuid, text, text, timestamptz, timestamptz, int), taskiem_storefront_lock(uuid),
  taskiem_storefront_changes_claim(int), taskiem_storefront_change_done(uuid), taskiem_storefront_booking_tenants(),
  taskiem_storefront_holds_sweep() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_storefront_holds_sweep();
DROP FUNCTION taskiem_storefront_booking_tenants();
DROP FUNCTION taskiem_storefront_change_done(uuid);
DROP FUNCTION taskiem_storefront_changes_claim(int);
DROP FUNCTION taskiem_public_change(text, bytea, text, int, timestamptz, timestamptz, timestamptz, int);
DROP FUNCTION taskiem_public_price_change(text, bytea);
DROP FUNCTION taskiem_public_booking(text, bytea);
DROP FUNCTION taskiem_public_storefront_services(uuid);
DROP FUNCTION taskiem_public_busy(uuid, timestamptz, timestamptz);
DROP FUNCTION taskiem_public_book(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int);
DROP FUNCTION taskiem_storefront_lock(uuid);
DROP FUNCTION taskiem_storefront_slot_free(uuid, text, text, timestamptz, timestamptz, int);
DROP TABLE storefront_changes;
DROP TABLE storefront_holds;
DROP POLICY dispatch_change ON storefront_orders;
DELETE FROM storefront_orders WHERE kind = 'booking' OR status IN ('no_show', 'price_changed');
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_booking_time;
DROP INDEX storefront_orders_bookings;
ALTER TABLE storefront_orders DROP COLUMN service_id, DROP COLUMN service_name, DROP COLUMN starts_at, DROP COLUMN ends_at,
  DROP COLUMN version, DROP COLUMN price_change, DROP COLUMN deposit_kobo, DROP COLUMN status_key;
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_status_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_status_check CHECK (status IN ('received', 'awaiting_payment', 'paid', 'accepted',
  'declined', 'expired', 'completed', 'cancelled', 'refund_due'));
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_kind_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_kind_check CHECK (kind IN ('order', 'enquiry', 'callback'));
DELETE FROM triggers WHERE storefront_action = 'booking';
ALTER TABLE triggers DROP CONSTRAINT triggers_storefront_action_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_storefront_action_check CHECK (storefront_action IN ('order', 'enquiry', 'callback'));
DELETE FROM storefront_actions WHERE kind = 'booking';
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_kind_check;
ALTER TABLE storefront_actions ADD CONSTRAINT storefront_actions_kind_check CHECK (kind IN ('order', 'enquiry', 'callback'));
DROP TABLE storefront_services;
ALTER TABLE storefronts DROP COLUMN bookings_sent_on, DROP COLUMN availability, DROP COLUMN services_columns, DROP COLUMN services_table;
