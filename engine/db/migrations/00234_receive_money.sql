-- Receive money with Taskiem (decision 0052; iswallet account numbers,
-- workstreams W2 to W4): a business's wallet under Taskiem's own iswallet
-- client, one per business. The row records what Taskiem made for it and
-- what it may show: the connection (iswallet/taskiem), the wallet, where
-- the money is held (segregated or pooled, chosen once), the business's
-- own "pay me" number, and the bank account it withdraws to. The
-- director's BVN and the business email are sent to iswallet once and kept
-- nowhere: neither is a column here.

-- +goose Up
CREATE TABLE receive_money (
  tenant_id          uuid PRIMARY KEY REFERENCES tenants(id),
  environment        text NOT NULL DEFAULT 'prod',
  connection_id      uuid NOT NULL REFERENCES connections(id),
  mode               text NOT NULL CHECK (mode IN ('live', 'test')),
  wallet_id          text NOT NULL CHECK (length(wallet_id) BETWEEN 1 AND 255),
  owner_type         text NOT NULL CHECK (owner_type IN ('client_end_user_business', 'client_end_business')),
  -- The business's own number: a permanent account on its wallet whose
  -- reference names no customer, so a transfer to it reaches To match.
  account_reference  text NOT NULL,
  account_number     text,
  bank_name          text,
  account_name       text,
  -- Why the number is not there yet, in iswallet's words (KYC_REQUIRED).
  account_problem    text,
  -- Where "Withdraw to my bank" pays: resolved by name enquiry, kept.
  payout_bank_code      text,
  payout_bank_name      text,
  payout_account_number text CHECK (payout_account_number IS NULL OR payout_account_number ~ '^[0-9]{10}$'),
  payout_account_name   text,
  -- Withdrawals of this much or more take two people (0: one); the
  -- approval rule taskiem_withdrawals says so.
  two_approvers_above_kobo bigint NOT NULL DEFAULT 0 CHECK (two_approvers_above_kobo >= 0),
  -- The workflow that withdraws (the withdraw-to-bank template), once made.
  withdraw_workflow_id  uuid REFERENCES workflows(id),
  created_by         text NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE receive_money ENABLE ROW LEVEL SECURITY;
ALTER TABLE receive_money FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON receive_money TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON receive_money TO taskiem_app;

-- +goose Down
DROP TABLE receive_money;
