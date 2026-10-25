-- Accounts a business has paid (docs/money-log.md#accounts-paid): for the
-- approvals warning "First payment to this account". When a payout to a
-- bank account completes in prod (pay_bank, or an item of pay_many), the
-- account is remembered by the pseudonym sealing derives from its digits
-- (keyed by the business's own key, so it says nothing without it) with
-- the bank's code where the step had one. No account number is stored.
-- +goose Up
CREATE TABLE payout_accounts (
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  account_key    text NOT NULL CHECK (length(account_key) BETWEEN 1 AND 100),
  bank_code      text NOT NULL DEFAULT '' CHECK (length(bank_code) <= 20),
  times          int NOT NULL DEFAULT 1 CHECK (times >= 1),
  first_paid_at  timestamptz NOT NULL DEFAULT now(),
  last_paid_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, account_key, bank_code)
);
ALTER TABLE payout_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE payout_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payout_accounts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON payout_accounts TO taskiem_app;
GRANT UPDATE (times, last_paid_at) ON payout_accounts TO taskiem_app;

-- +goose Down
DROP TABLE payout_accounts;
