-- An invoice's page shows the customer's own account number when they
-- have one (docs/spec/iswallet-account-numbers.md W3; decision 0052): a
-- customer the business bills regularly, or a school's student, pays into
-- the permanent number registered for them, and the money is counted to
-- their invoices by matching. A one-time number stays on offer beside it.
-- The definer reads the columns 00216 already grants the dispatch role.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_invoice_own_account(p_token_hash bytea)
RETURNS TABLE (account_number text, bank_name text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.account_number, COALESCE(a.bank_name, '')
    FROM customer_invoices i
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
    JOIN customer_accounts a ON a.tenant_id = i.tenant_id AND a.customer_id = i.customer_id
   WHERE i.page_token_hash = p_token_hash AND i.sent_at IS NOT NULL AND i.customer_id IS NOT NULL
   ORDER BY a.id DESC
   LIMIT 1
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_invoice_own_account(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_invoice_own_account(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_invoice_own_account(bytea) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_invoice_own_account(bytea);
