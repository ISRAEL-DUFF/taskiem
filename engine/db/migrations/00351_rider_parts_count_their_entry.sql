-- A rider's remittance part uses its money log entry as an invoice's,
-- an order's or a sale's part does (docs/dispatch.md): what is left of
-- the entry takes it off, so the same transfer cannot also be counted
-- towards an invoice or an order (the 00213 part trigger reads
-- taskiem_money_entry_counted), and dispatch matching caps a transfer at
-- what is left of it (an entry a counter sale took is not the rider's).
--
-- The dispatch role reads the parts' entry and amount only, as 00265
-- let it read stock_sales': a definer it owns
-- (taskiem_public_school_family_students, 00216) works out a customer's
-- credit with taskiem_money_entry_left, which now reads them. Nothing
-- names the rider or the remittance.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_entry_counted(p_entry uuid) RETURNS bigint
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT sum(p.amount_kobo) FROM customer_invoice_payments p WHERE p.entry_id = p_entry AND p.undoes IS NULL
                     AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)), 0)
       + COALESCE((SELECT sum(p.amount_kobo) FROM storefront_order_payments p WHERE p.entry_id = p_entry AND p.undoes IS NULL
                     AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)), 0)
       + COALESCE((SELECT sum(x.amount_kobo) FROM stock_sales x WHERE x.entry_id = p_entry), 0)
       + COALESCE((SELECT sum(r.amount_kobo) FROM rider_remittance_parts r WHERE r.entry_id = p_entry), 0)
$$;
-- +goose StatementEnd
CREATE POLICY dispatch ON rider_remittance_parts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (entry_id, amount_kobo) ON rider_remittance_parts TO taskiem_dispatch;

-- +goose Down
REVOKE SELECT (entry_id, amount_kobo) ON rider_remittance_parts FROM taskiem_dispatch;
DROP POLICY dispatch ON rider_remittance_parts;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_entry_counted(p_entry uuid) RETURNS bigint
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT sum(p.amount_kobo) FROM customer_invoice_payments p WHERE p.entry_id = p_entry AND p.undoes IS NULL
                     AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)), 0)
       + COALESCE((SELECT sum(p.amount_kobo) FROM storefront_order_payments p WHERE p.entry_id = p_entry AND p.undoes IS NULL
                     AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)), 0)
       + COALESCE((SELECT sum(x.amount_kobo) FROM stock_sales x WHERE x.entry_id = p_entry), 0)
$$;
-- +goose StatementEnd
