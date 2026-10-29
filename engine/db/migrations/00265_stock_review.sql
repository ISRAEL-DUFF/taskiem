-- Review fixes for stock (docs/spec/stock.md).
--
-- A sale's money counts like an invoice's or an order's: an entry a sale
-- took (its cash takings, a transfer or a terminal's sale matched to it)
-- has nothing left to count, so no invoice or order can be paid from it,
-- nor a customer's credit hold it.
--
-- A movement's change is sent under a key of the movement, the count it
-- read and its claim: claims counts them, so a change refused once at a
-- count is not answered from that refusal when the count comes back to it.
-- +goose Up
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
GRANT SELECT (entry_id, amount_kobo) ON stock_sales TO taskiem_dispatch;

ALTER TABLE stock_movements ADD COLUMN claims int NOT NULL DEFAULT 0;
GRANT UPDATE (claims) ON stock_movements TO taskiem_app;

-- +goose Down
REVOKE UPDATE (claims) ON stock_movements FROM taskiem_app;
ALTER TABLE stock_movements DROP COLUMN claims;
REVOKE SELECT (entry_id, amount_kobo) ON stock_sales FROM taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_entry_counted(p_entry uuid) RETURNS bigint
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT sum(p.amount_kobo) FROM customer_invoice_payments p WHERE p.entry_id = p_entry AND p.undoes IS NULL
                     AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)), 0)
       + COALESCE((SELECT sum(p.amount_kobo) FROM storefront_order_payments p WHERE p.entry_id = p_entry AND p.undoes IS NULL
                     AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)), 0)
$$;
-- +goose StatementEnd
