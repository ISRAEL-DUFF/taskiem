-- Paying one person, finished: the review's fixes (build-plan-gaps H7).
--
-- - Wallet to wallet is matched on the account number AND its bank: an
--   account number is unique only within its bank, so the same ten digits
--   at another bank are another account. A business's Taskiem wallet
--   number is kept, for this match only, as a key made from its bank's
--   code and its number (wallet_accounts.account_key, sha256), with its
--   wallet's id and mode; receive_money keeps the bank's code beside its
--   name. The dispatch role no longer reads receive_money at all (00650's
--   policy and column grants are taken back): the definer reads only
--   wallet_accounts, by the key of one account and bank, and answers only
--   that wallet's id. The asking business is the transaction's own scope
--   (taskiem_tenant_scope()), not an argument.
-- - A payment that pays back an order's refund names the order
--   (payments.refund_order): one in flight or paid at a time, capped at what
--   is owed, the order marked refunded once paid.
-- +goose Up
ALTER TABLE receive_money ADD COLUMN bank_code text CHECK (length(bank_code) <= 20);

CREATE TABLE wallet_accounts (
  tenant_id    uuid PRIMARY KEY REFERENCES tenants(id),
  account_key  text NOT NULL UNIQUE CHECK (account_key ~ '^[0-9a-f]{64}$'),
  wallet_id    text NOT NULL CHECK (length(wallet_id) BETWEEN 1 AND 255),
  mode         text NOT NULL CHECK (mode IN ('live', 'test')),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE wallet_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE wallet_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON wallet_accounts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON wallet_accounts TO taskiem_app;
-- The definer's own read: keys (never a number) and wallets' ids.
CREATE POLICY dispatch_wallet_key ON wallet_accounts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, account_key, wallet_id, mode) ON wallet_accounts TO taskiem_dispatch;

REVOKE SELECT (tenant_id, environment, mode, wallet_id, account_number) ON receive_money FROM taskiem_dispatch;
DROP POLICY dispatch_wallet_account ON receive_money;
DROP FUNCTION taskiem_wallet_for_account(uuid, text);

-- Another business's Taskiem wallet by an account number at a bank (its
-- code in the paying wallet's own bank list): that wallet's id, for a
-- transfer between wallets, and nothing else. Only an active business's,
-- in the asker's own mode, never the asker's own.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wallet_for_account(p_account text, p_bank_code text)
RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT w.wallet_id FROM wallet_accounts w JOIN tenants t ON t.id = w.tenant_id AND t.status = 'active'
   WHERE p_account ~ '^[0-9]{10}$' AND length(p_bank_code) BETWEEN 1 AND 20
     AND w.account_key = encode(sha256(convert_to(p_bank_code || ':' || p_account, 'UTF8')), 'hex')
     AND NOT (w.tenant_id = ANY (taskiem_tenant_scope()))
     AND w.mode = (SELECT o.mode FROM wallet_accounts o WHERE o.tenant_id = ANY (taskiem_tenant_scope()) LIMIT 1)
   LIMIT 1
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_wallet_for_account(text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_wallet_for_account(text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_wallet_for_account(text, text) TO taskiem_app;

ALTER TABLE payments ADD COLUMN refund_order text CHECK (length(refund_order) <= 80);
CREATE INDEX payments_refund_order ON payments (tenant_id, refund_order) WHERE refund_order IS NOT NULL;

-- +goose Down
DROP INDEX payments_refund_order;
ALTER TABLE payments DROP COLUMN refund_order;
DROP FUNCTION taskiem_wallet_for_account(text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_wallet_for_account(p_tenant uuid, p_account text)
RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT m.wallet_id FROM receive_money m JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
   WHERE p_account ~ '^[0-9]{10}$' AND m.account_number = p_account AND m.environment = 'prod'
     AND m.tenant_id <> p_tenant
     AND m.mode = (SELECT o.mode FROM receive_money o WHERE o.tenant_id = p_tenant)
   LIMIT 1
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_wallet_for_account(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_wallet_for_account(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_wallet_for_account(uuid, text) TO taskiem_app;
CREATE POLICY dispatch_wallet_account ON receive_money FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, environment, mode, wallet_id, account_number) ON receive_money TO taskiem_dispatch;
DROP TABLE wallet_accounts;
ALTER TABLE receive_money DROP COLUMN bank_code;
