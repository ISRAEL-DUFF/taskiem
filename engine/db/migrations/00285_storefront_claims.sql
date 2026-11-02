-- Claims (docs/spec/fronts.md §5.4, F5): one model for every
-- reservation. What a list item has is its count (canopies owned, seats,
-- a service's capacity); what is taken is a claim: the submission, the
-- item, the quantity, and a from and to. A claim waiting for payment or an
-- answer has expires_at; a confirmed one has none; releasing a
-- submission deletes its claim. A booking's slot hold was the first kind
-- of claim, so storefront_holds becomes storefront_claims with its rows:
-- service_id names the item (item_id), each claim takes qty of it, and
-- block_id names the list block whose calendar it is on (NULL: the
-- storefront's services, its booking calendar as before). The booking
-- functions keep their names and signatures and read the booking
-- calendar's claims.

-- +goose Up
ALTER TABLE storefront_holds RENAME TO storefront_claims;
ALTER TABLE storefront_claims RENAME COLUMN service_id TO item_id;
ALTER TABLE storefront_claims
  ADD COLUMN qty int NOT NULL DEFAULT 1 CHECK (qty > 0),
  ADD COLUMN block_id uuid REFERENCES storefront_blocks(id) ON DELETE CASCADE;
ALTER INDEX storefront_holds_time RENAME TO storefront_claims_time;
CREATE INDEX storefront_claims_block ON storefront_claims (block_id, item_id, starts_at) WHERE block_id IS NOT NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_slot_free(p_storefront uuid, p_except text, p_service text, p_start timestamptz, p_until timestamptz, p_capacity int)
RETURNS boolean
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  other int;
  same int;
BEGIN
  SELECT count(*) FILTER (WHERE h.item_id <> p_service OR h.starts_at <> p_start),
         count(*) FILTER (WHERE h.item_id = p_service AND h.starts_at = p_start)
    INTO other, same
    FROM storefront_claims h
   WHERE h.storefront_id = p_storefront AND h.reference IS DISTINCT FROM p_except AND h.block_id IS NULL
     AND (h.expires_at IS NULL OR h.expires_at > now())
     AND h.starts_at < p_until AND p_start < h.until_at;
  RETURN other = 0 AND same < greatest(p_capacity, 1);
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_busy(p_storefront uuid, p_from timestamptz, p_to timestamptz)
RETURNS TABLE (service_id text, starts_at timestamptz, until_at timestamptz, n int)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT h.item_id, h.starts_at, h.until_at, count(*)::int FROM storefront_claims h
    JOIN storefronts s ON s.id = h.storefront_id AND s.status = 'published'
   WHERE h.storefront_id = p_storefront AND (h.expires_at IS NULL OR h.expires_at > now()) AND h.block_id IS NULL
     AND h.starts_at < p_to AND p_from < h.until_at
   GROUP BY h.item_id, h.starts_at, h.until_at
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_holds_sweep()
RETURNS int
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH gone AS (DELETE FROM storefront_claims WHERE until_at < now() - interval '1 day' OR expires_at < now() - interval '1 day' RETURNING 1)
  SELECT count(*)::int FROM gone
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_book_action(p_action uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
  p_service text, p_start timestamptz, p_until timestamptz, p_capacity int)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  sf uuid;
  ref text;
BEGIN
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem;
  IF ref IS NOT NULL THEN
    RETURN ref;
  END IF;
  IF EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference) THEN
    RAISE EXCEPTION 'reference taken' USING ERRCODE = '23505', CONSTRAINT = 'storefront_inbox_reference_key';
  END IF;
  SELECT s.tenant_id, s.id INTO t, sf FROM storefront_actions a
    JOIN storefront_fronts f ON f.id = a.front_id AND f.enabled
    JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
    JOIN tenants tn ON tn.id = s.tenant_id AND tn.status = 'active'
   WHERE a.id = p_action AND a.enabled AND a.kind = 'booking';
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  IF p_until <= p_start OR p_start < now() THEN
    RAISE EXCEPTION 'slot taken' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_holds_slot';
  END IF;
  PERFORM taskiem_storefront_lock(sf);
  IF NOT taskiem_storefront_slot_free(sf, NULL, p_service, p_start, p_until, p_capacity) THEN
    RAISE EXCEPTION 'slot taken' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_holds_slot';
  END IF;
  INSERT INTO storefront_claims (reference, storefront_id, tenant_id, item_id, starts_at, until_at, expires_at)
    VALUES (p_reference, sf, t, p_service, p_start, p_until, now() + interval '10 minutes');
  INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload, action_id)
    VALUES (gen_random_uuid(), sf, t, 'booking', p_reference, p_idem, p_status_hash, p_payload, p_action);
  RETURN p_reference;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_change(p_reference text, p_status_hash bytea, p_action text, p_cutoff_minutes int,
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
      DELETE FROM storefront_claims WHERE reference = p_reference;
    END IF;
  ELSIF p_action IN ('cancel', 'move') THEN
    IF o.kind <> 'booking' OR o.status NOT IN ('received', 'accepted', 'awaiting_payment', 'paid') THEN
      RETURN 'not_allowed';
    END IF;
    IF now() > o.starts_at - make_interval(mins => greatest(p_cutoff_minutes, 0)) THEN
      RETURN 'too_late';
    END IF;
    IF p_action = 'cancel' THEN
      -- A paid booking is refunded, unless the booking form keeps the
      -- deposit when the customer cancels (options.cancel_refund).
      UPDATE storefront_orders x SET status = CASE WHEN x.status <> 'paid' THEN 'cancelled'
          WHEN (SELECT a.options->>'cancel_refund' FROM storefront_actions a WHERE a.id = x.action_id) = 'keep_deposit'
            THEN 'cancelled' ELSE 'refund_due' END,
        updated_at = now() WHERE x.id = o.id;
      DELETE FROM storefront_claims WHERE reference = p_reference;
    ELSE
      IF p_start IS NULL OR p_end <= p_start OR p_until < p_end OR p_start < now() THEN
        RETURN 'taken';
      END IF;
      PERFORM taskiem_storefront_lock(o.storefront_id);
      IF NOT taskiem_storefront_slot_free(o.storefront_id, p_reference, o.service_id, p_start, p_until, p_capacity) THEN
        RETURN 'taken';
      END IF;
      UPDATE storefront_claims SET starts_at = p_start, until_at = p_until WHERE reference = p_reference;
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

-- +goose Down
DROP INDEX storefront_claims_block;
ALTER INDEX storefront_claims_time RENAME TO storefront_holds_time;
DELETE FROM storefront_claims WHERE block_id IS NOT NULL;
ALTER TABLE storefront_claims DROP COLUMN block_id, DROP COLUMN qty;
ALTER TABLE storefront_claims RENAME COLUMN item_id TO service_id;
ALTER TABLE storefront_claims RENAME TO storefront_holds;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_slot_free(p_storefront uuid, p_except text, p_service text, p_start timestamptz, p_until timestamptz, p_capacity int)
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
CREATE OR REPLACE FUNCTION taskiem_public_busy(p_storefront uuid, p_from timestamptz, p_to timestamptz)
RETURNS TABLE (service_id text, starts_at timestamptz, until_at timestamptz, n int)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT h.service_id, h.starts_at, h.until_at, count(*)::int FROM storefront_holds h
    JOIN storefronts s ON s.id = h.storefront_id AND s.status = 'published'
   WHERE h.storefront_id = p_storefront AND (h.expires_at IS NULL OR h.expires_at > now())
     AND h.starts_at < p_to AND p_from < h.until_at
   GROUP BY h.service_id, h.starts_at, h.until_at
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_storefront_holds_sweep()
RETURNS int
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH gone AS (DELETE FROM storefront_holds WHERE until_at < now() - interval '1 day' OR expires_at < now() - interval '1 day' RETURNING 1)
  SELECT count(*)::int FROM gone
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_book_action(p_action uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
  p_service text, p_start timestamptz, p_until timestamptz, p_capacity int)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  sf uuid;
  ref text;
BEGIN
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem;
  IF ref IS NOT NULL THEN
    RETURN ref;
  END IF;
  IF EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference) THEN
    RAISE EXCEPTION 'reference taken' USING ERRCODE = '23505', CONSTRAINT = 'storefront_inbox_reference_key';
  END IF;
  SELECT s.tenant_id, s.id INTO t, sf FROM storefront_actions a
    JOIN storefront_fronts f ON f.id = a.front_id AND f.enabled
    JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
    JOIN tenants tn ON tn.id = s.tenant_id AND tn.status = 'active'
   WHERE a.id = p_action AND a.enabled AND a.kind = 'booking';
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  IF p_until <= p_start OR p_start < now() THEN
    RAISE EXCEPTION 'slot taken' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_holds_slot';
  END IF;
  PERFORM taskiem_storefront_lock(sf);
  IF NOT taskiem_storefront_slot_free(sf, NULL, p_service, p_start, p_until, p_capacity) THEN
    RAISE EXCEPTION 'slot taken' USING ERRCODE = '23P01', CONSTRAINT = 'storefront_holds_slot';
  END IF;
  INSERT INTO storefront_holds (reference, storefront_id, tenant_id, service_id, starts_at, until_at, expires_at)
    VALUES (p_reference, sf, t, p_service, p_start, p_until, now() + interval '10 minutes');
  INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload, action_id)
    VALUES (gen_random_uuid(), sf, t, 'booking', p_reference, p_idem, p_status_hash, p_payload, p_action);
  RETURN p_reference;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_change(p_reference text, p_status_hash bytea, p_action text, p_cutoff_minutes int,
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
      -- A paid booking is refunded, unless the booking form keeps the
      -- deposit when the customer cancels (options.cancel_refund).
      UPDATE storefront_orders x SET status = CASE WHEN x.status <> 'paid' THEN 'cancelled'
          WHEN (SELECT a.options->>'cancel_refund' FROM storefront_actions a WHERE a.id = x.action_id) = 'keep_deposit'
            THEN 'cancelled' ELSE 'refund_due' END,
        updated_at = now() WHERE x.id = o.id;
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
