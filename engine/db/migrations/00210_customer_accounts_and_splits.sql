-- Matching for customers' permanent accounts, for every business
-- (decision 0046 points 5 to 7; docs/spec/schools.md 7, B1;
-- docs/matching.md).
--
-- A customer is who an invoice is for: the business's own key for them
-- when it gives one (customer_key, such as "student:42"), else the
-- personal-data subject of their phone or email (subject_id, 00197).
-- customer_id is that identity, computed, never written.
--
-- customer_accounts links a customer's permanent account (a
-- virtual_account issued for them: the connection, the account number and
-- the account's reference) to the customer. A credit whose connection,
-- account number and reference agree with one is the customer's money:
-- money_entry_customers says so, its parts count towards the customer's
-- open invoices oldest first, and what is left is the customer's credit,
-- counted towards the next invoice sent to them.
--
-- One money log entry may now count towards several invoices and orders
-- (a split), each part its own payment row, undone on its own. The parts
-- of an entry never add up to more than it counts for (its amount, plus
-- its fee when the rail reported what landed): checked here, under the
-- entry's lock, whoever writes the row.
--
-- money_payers remembers who paid for which customer (sealed), so their
-- next payment is suggested for that customer first.
-- +goose Up
ALTER TABLE customer_invoices ADD COLUMN customer_key text CHECK (customer_key ~ '^[A-Za-z0-9:._/-]{1,100}$');
ALTER TABLE customer_invoices ADD COLUMN customer_id text GENERATED ALWAYS AS (COALESCE('key:' || customer_key, subject_id)) STORED;
CREATE INDEX customer_invoices_customer ON customer_invoices (tenant_id, customer_id);
GRANT UPDATE (customer_key) ON customer_invoices TO taskiem_app;

-- The account a credit was paid into, and whether amount_kobo is what
-- landed with the fee already taken (net) rather than what was sent.
ALTER TABLE money_entries ADD COLUMN account_number text CHECK (length(account_number) <= 40);
ALTER TABLE money_entries ADD COLUMN net boolean NOT NULL DEFAULT false;

-- Why matching held an entry for a person, when it did.
ALTER TABLE money_match_state ADD COLUMN held_reason text CHECK (length(held_reason) <= 300);

CREATE TABLE customer_accounts (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  customer_id     text NOT NULL,
  customer        jsonb NOT NULL,           -- sealed: {name, phone, email, key}
  channel         text NOT NULL CHECK (length(channel) <= 200),   -- paystack/main
  account_number  text NOT NULL CHECK (account_number ~ '^[0-9A-Za-z-]{4,40}$'),
  reference       text NOT NULL CHECK (length(reference) BETWEEN 1 AND 200),
  bank_name       text CHECK (length(bank_name) <= 120),
  created_by      text NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, channel, reference),
  UNIQUE (tenant_id, channel, account_number)
);
CREATE INDEX customer_accounts_customer ON customer_accounts (tenant_id, customer_id);
ALTER TABLE customer_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_accounts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON customer_accounts TO taskiem_app;

-- Entries that are a customer's money: paid into their account
-- (how account), or said to be theirs by a person who matched part of it
-- and left the rest as their credit (how person). What the entry counts
-- for less its parts is the customer's credit.
CREATE TABLE money_entry_customers (
  entry_id     uuid PRIMARY KEY REFERENCES money_entries(id),
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  customer_id  text NOT NULL,
  account_id   uuid REFERENCES customer_accounts(id),
  how          text NOT NULL CHECK (how IN ('account', 'person')),
  by           text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  CHECK ((how = 'account') = (account_id IS NOT NULL))
);
CREATE INDEX money_entry_customers_customer ON money_entry_customers (tenant_id, customer_id);
ALTER TABLE money_entry_customers ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_entry_customers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_entry_customers TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON money_entry_customers TO taskiem_app;

-- Payers remembered: a payer's name (normalised) or paying account, as
-- the pseudonym its sealing derives (payer_key), and the customer they
-- paid for. payer is what the rail sent, sealed.
CREATE TABLE money_payers (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  kind         text NOT NULL CHECK (kind IN ('name', 'account')),
  payer_key    text NOT NULL CHECK (length(payer_key) <= 200),
  payer        jsonb NOT NULL,
  customer_id  text NOT NULL,
  times        int NOT NULL DEFAULT 1 CHECK (times >= 1),
  first_by     text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, kind, payer_key, customer_id)
);
CREATE INDEX money_payers_customer ON money_payers (tenant_id, customer_id);
ALTER TABLE money_payers ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_payers FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_payers TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON money_payers TO taskiem_app;
GRANT UPDATE (payer, times, last_at) ON money_payers TO taskiem_app;

-- What an entry counts for: its amount, plus its fee when the amount is
-- what landed (engine/moneylog.CountableKobo says the same). NULL for an
-- entry with no amount.
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_entry_countable(p_amount bigint, p_fee bigint, p_net boolean) RETURNS bigint
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE WHEN p_amount IS NULL THEN NULL WHEN p_net THEN p_amount + COALESCE(p_fee, 0) ELSE p_amount END
$$;
-- +goose StatementEnd
-- What an entry's parts not undone add up to, invoices' and orders'.
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_entry_counted(p_entry uuid) RETURNS bigint
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT sum(p.amount_kobo) FROM customer_invoice_payments p WHERE p.entry_id = p_entry AND p.undoes IS NULL
                     AND NOT EXISTS (SELECT 1 FROM customer_invoice_payments u WHERE u.undoes = p.id)), 0)
       + COALESCE((SELECT sum(p.amount_kobo) FROM storefront_order_payments p WHERE p.entry_id = p_entry AND p.undoes IS NULL
                     AND NOT EXISTS (SELECT 1 FROM storefront_order_payments u WHERE u.undoes = p.id)), 0)
$$;
-- +goose StatementEnd
-- What is left of an entry to count (0 or more; NULL with no amount).
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_entry_left(p_entry uuid) RETURNS bigint
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT taskiem_money_entry_countable(e.amount_kobo, e.fee_kobo, e.net) - taskiem_money_entry_counted(e.id) FROM money_entries e WHERE e.id = p_entry
$$;
-- +goose StatementEnd

-- A part of an entry is refused when the parts would add up to more than
-- the entry counts for. The entry's lock (the one engine/invoicing and
-- engine/storefront take) makes concurrent parts wait for each other.
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_entry_part() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  countable bigint;
  counted bigint;
BEGIN
  IF NEW.entry_id IS NULL OR NEW.undoes IS NOT NULL THEN
    RETURN NEW;
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('taskiem money entry ' || NEW.entry_id::text, 0));
  SELECT taskiem_money_entry_countable(e.amount_kobo, e.fee_kobo, e.net) INTO countable FROM money_entries e WHERE e.id = NEW.entry_id;
  IF countable IS NULL THEN
    RETURN NEW; -- an entry with no amount: what the rail said is counted
  END IF;
  counted := taskiem_money_entry_counted(NEW.entry_id);
  IF counted + NEW.amount_kobo > countable THEN
    RAISE EXCEPTION 'the parts of a payment cannot add up to more than it (% counted of %, % more)', counted, countable, NEW.amount_kobo
      USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER invoice_payment_part BEFORE INSERT ON customer_invoice_payments
  FOR EACH ROW EXECUTE FUNCTION taskiem_money_entry_part();
CREATE TRIGGER order_payment_part BEFORE INSERT ON storefront_order_payments
  FOR EACH ROW EXECUTE FUNCTION taskiem_money_entry_part();

-- +goose Down
DROP TRIGGER order_payment_part ON storefront_order_payments;
DROP TRIGGER invoice_payment_part ON customer_invoice_payments;
DROP FUNCTION taskiem_money_entry_part();
DROP FUNCTION taskiem_money_entry_left(uuid);
DROP FUNCTION taskiem_money_entry_counted(uuid);
DROP FUNCTION taskiem_money_entry_countable(bigint, bigint, boolean);
DROP TABLE money_payers;
DROP TABLE money_entry_customers;
DROP TABLE customer_accounts;
ALTER TABLE money_match_state DROP COLUMN held_reason;
ALTER TABLE money_entries DROP COLUMN net;
ALTER TABLE money_entries DROP COLUMN account_number;
REVOKE UPDATE (customer_key) ON customer_invoices FROM taskiem_app;
DROP INDEX customer_invoices_customer;
ALTER TABLE customer_invoices DROP COLUMN customer_id;
ALTER TABLE customer_invoices DROP COLUMN customer_key;
