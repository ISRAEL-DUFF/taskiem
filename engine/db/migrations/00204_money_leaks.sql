-- Leaks, statements, cash and expenses (decision 0045; docs/spec/
-- invoices-and-money-log.md 7.4, 9.1, T6; docs/leaks.md, docs/money-log.md).
--
-- Entries people add (statement lines, cash, expenses) carry what they
-- typed: a description, and for expenses a vendor and a category, and
-- who entered them. A leak is money the business may be losing, found by
-- Taskiem and closed by a person as recovered (with the amount), written
-- off or not a leak. Fee rates are what the owner agreed with each
-- connection; a fee above them is a leak.
-- +goose Up
ALTER TABLE money_entries ADD COLUMN description text CHECK (length(description) <= 300);
ALTER TABLE money_entries ADD COLUMN vendor text CHECK (length(vendor) <= 120);
ALTER TABLE money_entries ADD COLUMN category text CHECK (length(category) <= 60);
ALTER TABLE money_entries ADD COLUMN entered_by text;

-- The column names a channel's statements use, as the owner matched them
-- to the common format once (date, description, amount, reference,
-- balance), kept for the next upload.
CREATE TABLE money_statement_formats (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  channel     text NOT NULL CHECK (length(channel) <= 200),
  columns     jsonb NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, channel)
);
ALTER TABLE money_statement_formats ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_statement_formats FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_statement_formats TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON money_statement_formats TO taskiem_app;

CREATE TABLE money_fee_rates (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  channel       text NOT NULL CHECK (length(channel) <= 200),
  percent_bp    int NOT NULL DEFAULT 0 CHECK (percent_bp BETWEEN 0 AND 10000), -- basis points of the amount
  flat_kobo     bigint NOT NULL DEFAULT 0 CHECK (flat_kobo >= 0),
  cap_kobo      bigint CHECK (cap_kobo >= 0),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, channel)
);
ALTER TABLE money_fee_rates ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_fee_rates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_fee_rates TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON money_fee_rates TO taskiem_app;

CREATE TABLE money_leaks (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  kind            text NOT NULL CHECK (kind IN ('unmatched', 'overdue', 'duplicate', 'overpaid', 'void_paid', 'fee')),
  -- What it is about, one leak per kind and subject: the entry, or the
  -- invoice.
  subject         text NOT NULL CHECK (length(subject) <= 200),
  entry_id        uuid REFERENCES money_entries(id),
  invoice_id      uuid REFERENCES customer_invoices(id),
  amount_kobo     bigint NOT NULL CHECK (amount_kobo >= 0),
  detail          text NOT NULL CHECK (length(detail) <= 300),
  status          text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'recovered', 'written_off', 'not_a_leak', 'resolved')),
  assigned_to     uuid,
  recovered_kobo  bigint CHECK (recovered_kobo >= 0),
  note            text CHECK (length(note) <= 500),
  opened_at       timestamptz NOT NULL DEFAULT now(),
  closed_at       timestamptz,
  closed_by       text,
  UNIQUE (tenant_id, kind, subject),
  CHECK ((status = 'open') = (closed_at IS NULL)),
  CHECK (status <> 'recovered' OR recovered_kobo IS NOT NULL)
);
CREATE INDEX money_leaks_open ON money_leaks (tenant_id, opened_at DESC) WHERE status = 'open';
ALTER TABLE money_leaks ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_leaks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_leaks TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON money_leaks TO taskiem_app;
GRANT UPDATE (status, assigned_to, recovered_kobo, note, closed_at, closed_by, amount_kobo, detail) ON money_leaks TO taskiem_app;

-- Tenants with money or invoices to look for leaks in.
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_leak_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM money_entries UNION SELECT tenant_id FROM customer_invoices WHERE sent_at IS NOT NULL
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_money_leak_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_money_leak_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_money_leak_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_money_leak_tenants();
DROP TABLE money_leaks;
DROP TABLE money_fee_rates;
DROP TABLE money_statement_formats;
ALTER TABLE money_entries DROP COLUMN entered_by;
ALTER TABLE money_entries DROP COLUMN category;
ALTER TABLE money_entries DROP COLUMN vendor;
ALTER TABLE money_entries DROP COLUMN description;
