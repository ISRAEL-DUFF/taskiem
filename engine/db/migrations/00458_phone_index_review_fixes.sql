-- The phone index, as review found it should be (taskiem-for-everyone §13;
-- stage 2 review fixes):
--
-- * Orders made with a phone before it was verified on the account, and
--   not yet confirmed ("These are mine"), are only counted
--   (taskiem_customer_phone_orders_waiting): nothing of them, not their
--   reference, state, total or business, is shown until confirmed, so a
--   recycled number never sees the previous owner's orders.
-- * The functions are the person's own: each refuses a person who is not
--   the transaction's (taskiem_person_scope()), so they are called in the
--   person's own scope.
-- * One business's orders, when asked (p_business), are chosen in the
--   function, before the limit.
-- * The one-time fill marks a business that failed (failed_at, tries) and
--   tries it again later, after the businesses not yet tried: one that
--   fails for good no longer holds back the rest. Orders it could not open
--   are counted (skipped), not a failure.
-- +goose Up
ALTER TABLE phone_index_backfill
  ALTER COLUMN done_at DROP NOT NULL,
  ADD COLUMN skipped int NOT NULL DEFAULT 0,
  ADD COLUMN tries int NOT NULL DEFAULT 0,
  ADD COLUMN failed_at timestamptz;
GRANT SELECT (done_at, failed_at, tries) ON phone_index_backfill TO taskiem_dispatch;

-- Not done yet: never tried first, then those that failed, each after an
-- hour per try (at most a day).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_phone_index_backfill_due(p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id FROM tenants t
    LEFT JOIN phone_index_backfill b ON b.tenant_id = t.id
   WHERE t.status <> 'deleted'
     AND EXISTS (SELECT 1 FROM storefront_orders o WHERE o.tenant_id = t.id)
     AND (b.tenant_id IS NULL
          OR (b.done_at IS NULL AND b.failed_at < now() - LEAST(GREATEST(b.tries, 1), 24) * interval '1 hour'))
   ORDER BY b.failed_at NULLS FIRST, t.id LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_customer_phone_orders(uuid, bytea, int);

-- A person's own confirmed storefront orders and bookings, across
-- businesses (or of one, p_business): the index rows for the hash of their
-- verified phone, and from each order only its reference, state, total,
-- date and business. Orders waiting for "These are mine" are left out.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_phone_orders(p_user uuid, p_key bytea, p_business uuid, p_limit int)
RETURNS TABLE (reference text, state text, total_kobo bigint, made_at timestamptz, business text, business_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
  SELECT o.reference, o.status, o.total_kobo, o.created_at, t.name, t.id
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active'
    JOIN customer_phone_index i ON i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'))
    JOIN storefront_orders o ON o.id = i.order_id AND o.tenant_id = i.tenant_id
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE p.user_id = p_user AND length(p_key) = 32
     AND (p_business IS NULL OR i.tenant_id = p_business)
     AND NOT (o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL)
   ORDER BY o.created_at DESC LIMIT LEAST(GREATEST(p_limit, 1), 200);
END
$$;
-- +goose StatementEnd

-- How many orders made with the person's phone before it was on their
-- account wait for "These are mine": a count, nothing of them.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_phone_orders_waiting(p_user uuid, p_key bytea)
RETURNS int LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_n int;
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  SELECT count(*) INTO v_n
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active'
    JOIN customer_phone_index i ON i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'))
    JOIN storefront_orders o ON o.id = i.order_id AND o.tenant_id = i.tenant_id
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE p.user_id = p_user AND length(p_key) = 32
     AND o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL;
  RETURN v_n;
END
$$;
-- +goose StatementEnd

-- "These are mine", for the person in scope only.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_phone_orders_confirm(p_user uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_user IS NULL OR p_user IS DISTINCT FROM taskiem_person_scope() THEN
    RAISE EXCEPTION 'a person''s orders are their own' USING ERRCODE = '42501';
  END IF;
  UPDATE user_phones p SET orders_confirmed_at = COALESCE(p.orders_confirmed_at, now()) WHERE p.user_id = p_user;
  RETURN FOUND;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_customer_phone_orders(uuid, bytea, uuid, int)', 'taskiem_customer_phone_orders_waiting(uuid, bytea)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_phone_orders_confirm(p_user uuid)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH u AS (UPDATE user_phones p SET orders_confirmed_at = COALESCE(p.orders_confirmed_at, now()) WHERE p.user_id = p_user RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM u)
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_customer_phone_orders_waiting(uuid, bytea);
DROP FUNCTION taskiem_customer_phone_orders(uuid, bytea, uuid, int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_phone_orders(p_user uuid, p_key bytea, p_limit int)
RETURNS TABLE (reference text, state text, total_kobo bigint, made_at timestamptz, business text, business_id uuid, waiting boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT o.reference, o.status, o.total_kobo, o.created_at, t.name, t.id,
         (o.created_at < p.verified_at AND p.orders_confirmed_at IS NULL)
    FROM user_phones p
    JOIN users u ON u.id = p.user_id AND u.status = 'active'
    JOIN customer_phone_index i ON i.phone_hash = sha256(p_key || convert_to('phone-index:' || p.number, 'UTF8'))
    JOIN storefront_orders o ON o.id = i.order_id AND o.tenant_id = i.tenant_id
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE p.user_id = p_user AND length(p_key) = 32
   ORDER BY o.created_at DESC LIMIT LEAST(GREATEST(p_limit, 1), 200)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_customer_phone_orders(uuid, bytea, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_customer_phone_orders(uuid, bytea, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_customer_phone_orders(uuid, bytea, int) TO taskiem_app;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_phone_index_backfill_due(p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id FROM tenants t
   WHERE t.status <> 'deleted'
     AND EXISTS (SELECT 1 FROM storefront_orders o WHERE o.tenant_id = t.id)
     AND NOT EXISTS (SELECT 1 FROM phone_index_backfill b WHERE b.tenant_id = t.id)
   ORDER BY t.id LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd
REVOKE SELECT (done_at, failed_at, tries) ON phone_index_backfill FROM taskiem_dispatch;
DELETE FROM phone_index_backfill WHERE done_at IS NULL;
ALTER TABLE phone_index_backfill
  DROP COLUMN failed_at,
  DROP COLUMN tries,
  DROP COLUMN skipped,
  ALTER COLUMN done_at SET NOT NULL;
