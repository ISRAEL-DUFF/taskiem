-- +goose Up
-- Refunds (decision 0036, accepted 2026-10-20): the owner confirms a
-- refund in chat or the web app and Taskiem makes it through the
-- business's Paystack connection; a refunded order is refunded. A booking
-- form may keep the deposit when the customer cancels a paid booking.

ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_status_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_status_check CHECK (status IN ('received', 'awaiting_payment', 'paid', 'accepted',
  'declined', 'expired', 'completed', 'cancelled', 'refund_due', 'no_show', 'price_changed', 'refunded'));
ALTER TABLE storefront_orders ADD COLUMN refunded_at timestamptz;

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
          WHEN (SELECT a.options->>'cancel_refund' FROM storefront_actions a WHERE a.storefront_id = x.storefront_id AND a.kind = x.kind) = 'keep_deposit'
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

-- +goose Down
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
UPDATE storefront_orders SET status = 'refund_due' WHERE status = 'refunded';
ALTER TABLE storefront_orders DROP COLUMN refunded_at;
ALTER TABLE storefront_orders DROP CONSTRAINT storefront_orders_status_check;
ALTER TABLE storefront_orders ADD CONSTRAINT storefront_orders_status_check CHECK (status IN ('received', 'awaiting_payment', 'paid', 'accepted',
  'declined', 'expired', 'completed', 'cancelled', 'refund_due', 'no_show', 'price_changed'));
