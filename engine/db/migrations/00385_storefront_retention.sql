-- Storefront submissions are kept for the business's retention and then
-- their personal fields erased (docs/spec/storefronts.md, Privacy:
-- "default 12 months"). Until now orders kept the sealed customer, its
-- subject and the status link's hash for good.
-- - storefronts.retention_days: the business's setting, 30 to 3650 days,
--   365 by default (PUT /v1/storefront).
-- - taskiem_storefront_orders_forget(p_limit) takes ended orders (declined,
--   expired, completed, cancelled, no-show, price changed, refunded) not
--   changed for that long, across businesses, and replaces their customer
--   with an envelope whose key never existed (it opens as erased, as any
--   erased customer does), their subject_id with a marker and their status
--   hash with nothing, so the status link no longer opens them. The
--   customer's subject is not shredded: it would erase their newer orders.
--   It answers each business and how many, for the audit; the api runs it
--   hourly. Orders still open (awaiting payment, refund due, out for
--   delivery) are left until they end.
-- The function is owned by taskiem_dispatch, which reads only the columns
-- granted here. Its index is 00386's.
-- +goose Up
ALTER TABLE storefronts ADD COLUMN retention_days int NOT NULL DEFAULT 365 CHECK (retention_days BETWEEN 30 AND 3650);
ALTER TABLE storefront_orders ADD COLUMN forgotten_at timestamptz;
GRANT UPDATE (retention_days) ON storefronts TO taskiem_app;
GRANT SELECT (retention_days) ON storefronts TO taskiem_dispatch;
CREATE POLICY dispatch_forget ON storefront_orders FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (updated_at, forgotten_at), UPDATE (customer, subject_id, status_hash, forgotten_at) ON storefront_orders TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_orders_forget(p_limit int)
RETURNS TABLE (tenant_id uuid, forgotten int)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH f AS (
    UPDATE storefront_orders o
       SET customer = jsonb_build_object('$pii', 'customer', 'subject', 'retention:erased', 'ct', ''),
           subject_id = 'retention:erased', status_hash = '\x'::bytea, forgotten_at = now()
     WHERE o.id IN (
       SELECT x.id FROM storefront_orders x JOIN storefronts s ON s.id = x.storefront_id
        WHERE x.forgotten_at IS NULL
          AND x.status IN ('declined', 'expired', 'completed', 'cancelled', 'no_show', 'price_changed', 'refunded')
          AND x.updated_at < now() - make_interval(days => s.retention_days)
        ORDER BY x.updated_at LIMIT LEAST(GREATEST(p_limit, 1), 1000)
        FOR UPDATE OF x SKIP LOCKED)
    RETURNING o.tenant_id)
  SELECT f.tenant_id, count(*)::int FROM f GROUP BY f.tenant_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_storefront_orders_forget(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_storefront_orders_forget(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_storefront_orders_forget(int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_storefront_orders_forget(int);
REVOKE SELECT (updated_at, forgotten_at), UPDATE (customer, subject_id, status_hash, forgotten_at) ON storefront_orders FROM taskiem_dispatch;
DROP POLICY dispatch_forget ON storefront_orders;
REVOKE SELECT (retention_days) ON storefronts FROM taskiem_dispatch;
ALTER TABLE storefront_orders DROP COLUMN forgotten_at;
ALTER TABLE storefronts DROP COLUMN retention_days;
