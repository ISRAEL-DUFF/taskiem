-- The phone index (docs/spec/taskiem-for-everyone.md §13, §14, §19; stage
-- 2, stream B): which storefront orders and bookings were made with a
-- phone, across businesses, so a person whose phone is verified finds
-- their own ("Chat with {business} about this order").
--
-- A phone's personal-data subject differs in every business, so the index
-- keeps a keyed hash of the phone made with a platform key instead: the
-- key the API holds (TASKIEM_MESSAGE_HASH_KEY), narrowed to this use
-- (engine/phoneindex: sha256(index key || 'phone-index:' || E.164)). No
-- number, name or amount is kept here: the hash, the business, the order.
--
-- * Written by the API in the transaction that makes the order; removed
--   when the order is deleted (the foreign key), when the order's customer
--   is erased (their subject's key shredded) and when retention forgets
--   the order's customer.
-- * phone_index_backfill marks the businesses whose existing orders the
--   one-time job has indexed; taskiem_phone_index_backfill_due lists the
--   others, ids only.
-- * Read only through taskiem_customer_phone_orders: given a person, it
--   reads their verified phone itself (user_phones), so it answers no
--   phone but theirs, and returns for each order only its reference,
--   state, total, date, the business's name and id, and whether it was
--   made before the phone was verified on the account and is not yet
--   confirmed as theirs ("These are mine",
--   taskiem_customer_phone_orders_confirm). A phone change forgets the
--   confirmation.

-- +goose Up
CREATE TABLE customer_phone_index (
  order_id    uuid PRIMARY KEY REFERENCES storefront_orders(id) ON DELETE CASCADE,
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  phone_hash  bytea NOT NULL CHECK (length(phone_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_phone_index_phone ON customer_phone_index (phone_hash);
CREATE INDEX customer_phone_index_tenant ON customer_phone_index (tenant_id);
ALTER TABLE customer_phone_index ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_phone_index FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_phone_index TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_phone_index TO taskiem_app;
CREATE POLICY dispatch ON customer_phone_index TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (order_id, tenant_id, phone_hash), DELETE ON customer_phone_index TO taskiem_dispatch;

CREATE TABLE phone_index_backfill (
  tenant_id  uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  indexed    int NOT NULL DEFAULT 0,
  done_at    timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE phone_index_backfill ENABLE ROW LEVEL SECURITY;
ALTER TABLE phone_index_backfill FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON phone_index_backfill TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON phone_index_backfill TO taskiem_app;
CREATE POLICY dispatch ON phone_index_backfill FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id) ON phone_index_backfill TO taskiem_dispatch;

ALTER TABLE user_phones ADD COLUMN orders_confirmed_at timestamptz;

-- A new number is not confirmed as anyone's orders yet.
-- +goose StatementBegin
CREATE FUNCTION taskiem_user_phone_changed() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF NEW.number IS DISTINCT FROM OLD.number THEN
    NEW.orders_confirmed_at := NULL;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_user_phone_changed() FROM PUBLIC;
CREATE TRIGGER user_phone_changed BEFORE UPDATE OF number ON user_phones
  FOR EACH ROW EXECUTE FUNCTION taskiem_user_phone_changed();

-- Erasing a customer (their subject's key shredded, in the business's own
-- transaction) takes their orders out of the index.
-- +goose StatementBegin
CREATE FUNCTION taskiem_phone_index_erased() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM customer_phone_index i USING storefront_orders o
   WHERE o.id = i.order_id AND o.tenant_id = NEW.tenant_id AND o.subject_id = NEW.subject_id AND i.tenant_id = NEW.tenant_id;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_phone_index_erased() FROM PUBLIC;
CREATE TRIGGER phone_index_erased AFTER UPDATE OF shredded_at ON subject_keys
  FOR EACH ROW WHEN (OLD.shredded_at IS NULL AND NEW.shredded_at IS NOT NULL) EXECUTE FUNCTION taskiem_phone_index_erased();

-- Retention forgetting an order's customer (taskiem_storefront_orders_forget,
-- run as taskiem_dispatch) takes the order out of the index too.
-- +goose StatementBegin
CREATE FUNCTION taskiem_phone_index_forgotten() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM customer_phone_index i WHERE i.order_id = NEW.id;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_phone_index_forgotten() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_phone_index_forgotten() FROM PUBLIC;
CREATE TRIGGER phone_index_forgotten AFTER UPDATE OF forgotten_at ON storefront_orders
  FOR EACH ROW WHEN (OLD.forgotten_at IS NULL AND NEW.forgotten_at IS NOT NULL) EXECUTE FUNCTION taskiem_phone_index_forgotten();

-- The businesses whose existing orders the one-time job has still to
-- index: ids only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_phone_index_backfill_due(p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id FROM tenants t
   WHERE t.status <> 'deleted'
     AND EXISTS (SELECT 1 FROM storefront_orders o WHERE o.tenant_id = t.id)
     AND NOT EXISTS (SELECT 1 FROM phone_index_backfill b WHERE b.tenant_id = t.id)
   ORDER BY t.id LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd

-- A person's own storefront orders and bookings, across businesses: the
-- index rows for the hash of their verified phone (made here, from
-- user_phones, with the index key the API gives), and from each order
-- only its reference, state, total, date and business. waiting marks an
-- order made before the phone was verified on the account and not yet
-- confirmed as theirs. Newest first, at most p_limit (up to 200).
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

-- "These are mine": the person confirms the orders made with their phone
-- before it was verified on their account. False when they have no phone.
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_phone_orders_confirm(p_user uuid)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH u AS (UPDATE user_phones p SET orders_confirmed_at = COALESCE(p.orders_confirmed_at, now()) WHERE p.user_id = p_user RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM u)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_phone_index_backfill_due(int)', 'taskiem_customer_phone_orders(uuid, bytea, int)',
    'taskiem_customer_phone_orders_confirm(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_customer_phone_orders_confirm(uuid);
DROP FUNCTION taskiem_customer_phone_orders(uuid, bytea, int);
DROP FUNCTION taskiem_phone_index_backfill_due(int);
DROP TRIGGER phone_index_forgotten ON storefront_orders;
DROP FUNCTION taskiem_phone_index_forgotten();
DROP TRIGGER phone_index_erased ON subject_keys;
DROP FUNCTION taskiem_phone_index_erased();
DROP TRIGGER user_phone_changed ON user_phones;
DROP FUNCTION taskiem_user_phone_changed();
ALTER TABLE user_phones DROP COLUMN orders_confirmed_at;
DROP TABLE phone_index_backfill;
DROP TABLE customer_phone_index;
