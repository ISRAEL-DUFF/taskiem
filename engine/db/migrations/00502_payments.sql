-- Paying one person (P1; docs/spec/chat-pay-people.md): a payment is a
-- record first and a workflow run second. The chat, Tools or a workflow
-- prepares it, to a business's saved bank account (a saved object of the
-- kind bank_account: the payee) or, for an owner, their own; a person who
-- may pay (money.pay) confirms it with a passkey; the business's managed
-- pay-person workflow checks the name, asks the owner's OK when the
-- payment is over the payer's limit or the account changed since it was
-- last paid, pays with the payment's own key, waits for the bank and
-- finishes the record (payments@1 finish).
-- +goose Up
CREATE TABLE payments (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  -- The payee: the business's saved account it was prepared from (none
  -- for an owner's own, whose object the business cannot read: personal),
  -- and a copy of where the money goes, kept with the payment as a record.
  saved_id        uuid REFERENCES saved_objects(id) ON DELETE SET NULL,
  personal        boolean NOT NULL DEFAULT false,
  payee_title     text NOT NULL CHECK (length(payee_title) BETWEEN 1 AND 120),
  bank_name       text NOT NULL CHECK (length(bank_name) BETWEEN 1 AND 120),
  bank_code       text NOT NULL CHECK (length(bank_code) BETWEEN 1 AND 20),
  account_sealed  jsonb NOT NULL,
  last4           text NOT NULL CHECK (last4 ~ '^[0-9]{4}$'),
  account_name    text,
  amount_kobo     bigint NOT NULL CHECK (amount_kobo > 0),
  -- What it is for, as the money log keeps it.
  purpose         text NOT NULL CHECK (purpose IN ('bill', 'wages', 'delivery', 'refund', 'stock', 'other')),
  purpose_note    text NOT NULL DEFAULT '' CHECK (length(purpose_note) <= 80),
  note            text NOT NULL DEFAULT '' CHECK (length(note) <= 100),
  -- What the checks found when it was prepared, and why the owner's OK
  -- is asked (over a limit, a changed account), if it is.
  first_payment   boolean NOT NULL DEFAULT false,
  changed_account boolean NOT NULL DEFAULT false,
  needs_ok        text NOT NULL DEFAULT '' CHECK (needs_ok IN ('', 'limit', 'changed', 'limit,changed')),
  status          text NOT NULL DEFAULT 'prepared'
                  CHECK (status IN ('prepared', 'sending', 'waiting_ok', 'paid', 'failed', 'rejected', 'unknown', 'cancelled')),
  reason          text,
  fee_kobo        bigint,
  payout_ref      text,
  run_id          uuid,
  started_from    text NOT NULL DEFAULT 'tools' CHECK (started_from IN ('tools', 'chat', 'message', 'card', 'workflow')),
  prepared_by     text NOT NULL,
  confirmed_by    text,
  created_at      timestamptz NOT NULL DEFAULT now(),
  confirmed_at    timestamptz,
  finished_at     timestamptz
);
CREATE INDEX payments_recent ON payments (tenant_id, created_at DESC);
CREATE INDEX payments_saved ON payments (tenant_id, saved_id, created_at DESC);
ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payments TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON payments TO taskiem_app;

-- What each role may pay without the owner's OK: one payment and a day's
-- total, in kobo (0: every payment needs the OK). An owner has no limit;
-- a role with no row has 0.
CREATE TABLE pay_limits (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  role        text NOT NULL CHECK (length(role) BETWEEN 1 AND 64),
  one_kobo    bigint NOT NULL DEFAULT 0 CHECK (one_kobo >= 0),
  day_kobo    bigint NOT NULL DEFAULT 0 CHECK (day_kobo >= 0),
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, role)
);
ALTER TABLE pay_limits ENABLE ROW LEVEL SECURITY;
ALTER TABLE pay_limits FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pay_limits TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON pay_limits TO taskiem_app;

-- How a business pays people: the managed workflow made from the
-- pay-person template, and what it was made with.
CREATE TABLE pay_settings (
  tenant_id        uuid PRIMARY KEY REFERENCES tenants(id),
  workflow_id      uuid NOT NULL,
  connection       text NOT NULL,
  funding_account  text NOT NULL DEFAULT '',
  updated_by       text NOT NULL,
  updated_at       timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE pay_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE pay_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pay_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON pay_settings TO taskiem_app;

-- +goose Down
DROP TABLE pay_settings;
DROP TABLE pay_limits;
DROP TABLE payments;
