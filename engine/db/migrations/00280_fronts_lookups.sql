-- Fronts F1c (docs/spec/fronts.md §5.3, §9): every definer that found an
-- action by (storefront_id, kind) finds it by id. A submission is written
-- through taskiem_public_submit_action and taskiem_public_book_action,
-- which take the action's id and record it on the inbox row; the old
-- signatures stay for a release as wrappers that resolve the built-in
-- action of the kind, so a public host on the old binary keeps working
-- while a deploy rolls out. What an order reads of its form (the card
-- connection, cancel_refund) is read from the order's own action.

-- +goose Up

-- A submission to an action: refused (NULL) unless the action, its front
-- and its storefront are open and the business is active. A repeated
-- idempotency token returns the first reference; a reference an order
-- already has is taken (23505), and the public host draws another.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_submit_action(p_action uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  sf uuid;
  k text;
  ref text;
BEGIN
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem;
  IF ref IS NOT NULL THEN
    RETURN ref;
  END IF;
  IF EXISTS (SELECT 1 FROM storefront_orders o WHERE o.reference = p_reference) THEN
    RAISE EXCEPTION 'reference taken' USING ERRCODE = '23505', CONSTRAINT = 'storefront_inbox_reference_key';
  END IF;
  SELECT s.tenant_id, s.id, a.kind INTO t, sf, k FROM storefront_actions a
    JOIN storefront_fronts f ON f.id = a.front_id AND f.enabled
    JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
    JOIN tenants tn ON tn.id = s.tenant_id AND tn.status = 'active'
   WHERE a.id = p_action AND a.enabled AND a.kind <> 'booking';
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  INSERT INTO storefront_inbox (id, storefront_id, tenant_id, kind, reference, idem, status_hash, payload, action_id)
    VALUES (gen_random_uuid(), sf, t, k, p_reference, p_idem, p_status_hash, p_payload, p_action)
    ON CONFLICT (idem) DO NOTHING;
  SELECT reference INTO ref FROM storefront_inbox WHERE idem = p_idem;
  RETURN ref;
END
$$;
-- +goose StatementEnd

-- A booking to a booking action: holds the slot and writes the submission
-- in one transaction, as taskiem_public_book did.
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_book_action(p_action uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
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

-- The old signatures, for one release: the built-in action of the kind.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_submit(p_storefront uuid, p_kind text, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a uuid;
BEGIN
  SELECT x.id INTO a FROM storefront_actions x WHERE x.storefront_id = p_storefront AND x.kind = p_kind AND x.builtin;
  IF a IS NULL THEN
    RETURN NULL;
  END IF;
  RETURN taskiem_public_submit_action(a, p_reference, p_idem, p_status_hash, p_payload);
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_book(p_storefront uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
  p_service text, p_start timestamptz, p_until timestamptz, p_capacity int)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  a uuid;
BEGIN
  SELECT x.id INTO a FROM storefront_actions x WHERE x.storefront_id = p_storefront AND x.kind = 'booking' AND x.builtin;
  IF a IS NULL THEN
    RETURN NULL;
  END IF;
  RETURN taskiem_public_book_action(a, p_reference, p_idem, p_status_hash, p_payload, p_service, p_start, p_until, p_capacity);
END
$$;
-- +goose StatementEnd

-- The api role's side of the inbox, naming each submission's action (the
-- built-in action of its kind for a row an old public host wrote).
DROP FUNCTION taskiem_storefront_inbox_claim(int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_inbox_claim(p_limit int)
RETURNS TABLE (id uuid, storefront_id uuid, tenant_id uuid, kind text, reference text, status_hash bytea, payload bytea, created_at timestamptz, action_id uuid)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE storefront_inbox i SET claimed_until = now() + interval '2 minutes'
   WHERE i.id IN (SELECT x.id FROM storefront_inbox x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING i.id, i.storefront_id, i.tenant_id, i.kind, i.reference, i.status_hash, i.payload, i.created_at,
    COALESCE(i.action_id, (SELECT a.id FROM storefront_actions a WHERE a.storefront_id = i.storefront_id AND a.kind = i.kind AND a.builtin))
$$;
-- +goose StatementEnd

-- A customer's change to their booking: the deposit kept or not by the
-- booking's own action.
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

-- An order's status page's transfer part: the card connection from the
-- order's own action.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_order_payment(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, amount_kobo bigint, account_number text, bank_name text, account_name text, expires_at timestamptz, on_late text,
               on_wrong_amount text, waiting boolean, issued_last_hour int, issued int, card boolean, held_until timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH ord AS (
    SELECT o.id, o.expires_at, COALESCE(a.options->>'connection', '') AS connection FROM storefront_orders o
      JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active'
      LEFT JOIN storefront_actions a ON a.id = o.action_id
     WHERE o.reference = p_reference AND o.status_hash = p_status_hash
  )
  SELECT a.status, a.amount_kobo, a.account_number, a.bank_name, a.account_name, a.expires_at, a.on_late, a.on_wrong_amount,
         EXISTS (SELECT 1 FROM customer_invoice_requests r WHERE r.order_id = ord.id AND r.kind = 'transfer'),
         (SELECT count(*)::int FROM customer_invoice_accounts c WHERE c.order_id = ord.id AND c.kind = 'transfer' AND c.created_at > now() - interval '1 hour'),
         (SELECT count(*)::int FROM customer_invoice_accounts c WHERE c.order_id = ord.id AND c.kind = 'transfer'),
         ord.connection NOT LIKE '%/%' OR ord.connection LIKE 'paystack/%', ord.expires_at
    FROM ord
    LEFT JOIN LATERAL (SELECT * FROM customer_invoice_accounts a WHERE a.order_id = ord.id AND a.kind = 'transfer' ORDER BY a.seq DESC LIMIT 1) a ON true
$$;
-- +goose StatementEnd

-- A storefront trigger deployed before its storefront had the action it
-- names by kind is linked to that action when the action is made; one an
-- older binary registers is linked as it is written.
UPDATE triggers t SET storefront_action_id = a.id
  FROM storefront_actions a WHERE t.type = 'storefront' AND t.storefront_action_id IS NULL
   AND a.tenant_id = t.tenant_id AND a.kind = t.storefront_action AND a.builtin;
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_trigger_fill() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.type = 'storefront' AND NEW.storefront_action_id IS NULL AND NEW.storefront_action IS NOT NULL THEN
    SELECT a.id INTO NEW.storefront_action_id FROM storefront_actions a
     WHERE a.tenant_id = NEW.tenant_id AND a.kind = NEW.storefront_action AND a.builtin;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_action_link() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  UPDATE triggers SET storefront_action_id = NEW.id
   WHERE tenant_id = NEW.tenant_id AND type = 'storefront' AND storefront_action = NEW.kind AND storefront_action_id IS NULL;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER triggers_storefront_fill BEFORE INSERT ON triggers FOR EACH ROW EXECUTE FUNCTION taskiem_storefront_trigger_fill();
CREATE TRIGGER storefront_actions_link AFTER INSERT ON storefront_actions FOR EACH ROW WHEN (NEW.builtin) EXECUTE FUNCTION taskiem_storefront_action_link();

ALTER FUNCTION taskiem_public_submit_action(uuid, text, bytea, bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_book_action(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_storefront_inbox_claim(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_submit_action(uuid, text, bytea, bytea, bytea),
  taskiem_public_book_action(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int), taskiem_storefront_inbox_claim(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_submit_action(uuid, text, bytea, bytea, bytea),
  taskiem_public_book_action(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_storefront_inbox_claim(int) TO taskiem_app;

-- +goose Down
DROP TRIGGER storefront_actions_link ON storefront_actions;
DROP TRIGGER triggers_storefront_fill ON triggers;
DROP FUNCTION taskiem_storefront_action_link();
DROP FUNCTION taskiem_storefront_trigger_fill();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_order_payment(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, amount_kobo bigint, account_number text, bank_name text, account_name text, expires_at timestamptz, on_late text,
               on_wrong_amount text, waiting boolean, issued_last_hour int, issued int, card boolean, held_until timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH ord AS (
    SELECT o.id, o.expires_at, COALESCE(a.options->>'connection', '') AS connection FROM storefront_orders o
      JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active'
      LEFT JOIN storefront_actions a ON a.storefront_id = o.storefront_id AND a.kind = o.kind
     WHERE o.reference = p_reference AND o.status_hash = p_status_hash
  )
  SELECT a.status, a.amount_kobo, a.account_number, a.bank_name, a.account_name, a.expires_at, a.on_late, a.on_wrong_amount,
         EXISTS (SELECT 1 FROM customer_invoice_requests r WHERE r.order_id = ord.id AND r.kind = 'transfer'),
         (SELECT count(*)::int FROM customer_invoice_accounts c WHERE c.order_id = ord.id AND c.kind = 'transfer' AND c.created_at > now() - interval '1 hour'),
         (SELECT count(*)::int FROM customer_invoice_accounts c WHERE c.order_id = ord.id AND c.kind = 'transfer'),
         ord.connection NOT LIKE '%/%' OR ord.connection LIKE 'paystack/%', ord.expires_at
    FROM ord
    LEFT JOIN LATERAL (SELECT * FROM customer_invoice_accounts a WHERE a.order_id = ord.id AND a.kind = 'transfer' ORDER BY a.seq DESC LIMIT 1) a ON true
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
DROP FUNCTION taskiem_storefront_inbox_claim(int);
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
ALTER FUNCTION taskiem_storefront_inbox_claim(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefront_inbox_claim(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_storefront_inbox_claim(int) TO taskiem_app;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_book(p_storefront uuid, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea,
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_submit(p_storefront uuid, p_kind text, p_reference text, p_idem bytea, p_status_hash bytea, p_payload bytea)
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
DROP FUNCTION taskiem_public_book_action(uuid, text, bytea, bytea, bytea, text, timestamptz, timestamptz, int);
DROP FUNCTION taskiem_public_submit_action(uuid, text, bytea, bytea, bytea);
