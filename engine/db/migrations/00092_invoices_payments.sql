-- Invoices and payments (spec 16, decision 0017).
--
-- Invoices are numbered without gaps per year (TKM-2026-000001: the number
-- is taken in the transaction that issues the invoice) and immutable once
-- issued: only their status may move (open -> paid, void or uncollectible;
-- paid and void are final), and they are never deleted. Amounts are kobo
-- (NGN); VAT is a separate amount at the rate in force when issued.
--
-- Payments are attempts to pay an invoice through a provider (Paystack,
-- Flutterwave), keyed by our reference, which embeds the tenant id so a
-- provider's webhook reaches the right tenant without a cross-tenant
-- lookup. Webhook receipts make deliveries idempotent.

-- +goose Up
CREATE TABLE billing_invoice_counters (
  year  int PRIMARY KEY,
  last  bigint NOT NULL
);
ALTER TABLE billing_invoice_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_invoice_counters FORCE ROW LEVEL SECURITY;
-- Only taskiem_next_invoice_number reaches it; the application role has no
-- grant, and its policy admits nothing.
CREATE POLICY none ON billing_invoice_counters TO taskiem_app USING (false);
CREATE POLICY dispatch ON billing_invoice_counters TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE ON billing_invoice_counters TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_next_invoice_number(p_prefix text, p_at timestamptz)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_year int := extract(year FROM p_at AT TIME ZONE 'UTC')::int;
  v_n bigint;
BEGIN
  IF p_prefix !~ '^[A-Z][A-Z0-9]{1,9}$' THEN
    RAISE EXCEPTION 'invoice prefix %: 2-10 capital letters or digits', p_prefix USING ERRCODE = '22023';
  END IF;
  INSERT INTO billing_invoice_counters (year, last) VALUES (v_year, 1)
    ON CONFLICT (year) DO UPDATE SET last = billing_invoice_counters.last + 1
    RETURNING last INTO v_n;
  RETURN p_prefix || '-' || v_year || '-' || lpad(v_n::text, 6, '0');
END
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_next_invoice_number(text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_next_invoice_number(text, timestamptz) TO taskiem_app;

CREATE TABLE invoices (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  number           text NOT NULL UNIQUE,
  kind             text NOT NULL CHECK (kind IN ('subscription', 'renewal', 'upgrade')),
  plan_id          text NOT NULL REFERENCES plans(id),
  billing_interval text NOT NULL CHECK (billing_interval IN ('monthly', 'annual')),
  period_start     timestamptz NOT NULL,
  period_end       timestamptz NOT NULL,
  currency         text NOT NULL DEFAULT 'NGN' CHECK (currency = 'NGN'),
  lines            jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array'),
  subtotal_kobo    bigint NOT NULL CHECK (subtotal_kobo >= 0),
  vat_rate_bp      int NOT NULL CHECK (vat_rate_bp >= 0 AND vat_rate_bp <= 10000), -- basis points: 750 = 7.5%
  vat_kobo         bigint NOT NULL CHECK (vat_kobo >= 0),
  total_kobo       bigint NOT NULL CHECK (total_kobo = subtotal_kobo + vat_kobo),
  bill_to          jsonb NOT NULL DEFAULT '{}',        -- tenant name and billing email when issued
  meta             jsonb NOT NULL DEFAULT '{}',        -- e.g. an upgrade's target plan
  status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'paid', 'void', 'uncollectible')),
  issued_at        timestamptz NOT NULL,
  due_at           timestamptz NOT NULL,
  paid_at          timestamptz,
  paid_reference   text,
  closed_reason    text
);
CREATE INDEX invoices_tenant ON invoices (tenant_id, issued_at DESC);
ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoices TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON invoices TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_invoice_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'invoices are never deleted (void them)' USING ERRCODE = '42501';
  END IF;
  IF (NEW.id, NEW.tenant_id, NEW.number, NEW.kind, NEW.plan_id, NEW.billing_interval, NEW.period_start, NEW.period_end, NEW.currency,
      NEW.lines, NEW.subtotal_kobo, NEW.vat_rate_bp, NEW.vat_kobo, NEW.total_kobo, NEW.bill_to, NEW.meta, NEW.issued_at, NEW.due_at)
     IS DISTINCT FROM
     (OLD.id, OLD.tenant_id, OLD.number, OLD.kind, OLD.plan_id, OLD.billing_interval, OLD.period_start, OLD.period_end, OLD.currency,
      OLD.lines, OLD.subtotal_kobo, OLD.vat_rate_bp, OLD.vat_kobo, OLD.total_kobo, OLD.bill_to, OLD.meta, OLD.issued_at, OLD.due_at) THEN
    RAISE EXCEPTION 'invoice % is issued and cannot change', OLD.number USING ERRCODE = '42501';
  END IF;
  IF OLD.status <> 'open' AND NEW.status IS DISTINCT FROM OLD.status
     AND NOT (OLD.status = 'uncollectible' AND NEW.status = 'paid') THEN
    RAISE EXCEPTION 'invoice % is % and final', OLD.number, OLD.status USING ERRCODE = '42501';
  END IF;
  IF OLD.status IN ('paid', 'void') AND (NEW.paid_at, NEW.paid_reference, NEW.closed_reason) IS DISTINCT FROM (OLD.paid_at, OLD.paid_reference, OLD.closed_reason) THEN
    RAISE EXCEPTION 'invoice % is % and final', OLD.number, OLD.status USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER invoices_immutable BEFORE UPDATE OR DELETE ON invoices
  FOR EACH ROW EXECUTE FUNCTION taskiem_invoice_immutable();

CREATE TABLE billing_payments (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  invoice_id       uuid NOT NULL REFERENCES invoices(id),
  provider         text NOT NULL CHECK (provider IN ('paystack', 'flutterwave')),
  reference        text NOT NULL UNIQUE,
  method           text NOT NULL CHECK (method IN ('checkout', 'authorization')),
  amount_kobo      bigint NOT NULL CHECK (amount_kobo > 0),
  currency         text NOT NULL DEFAULT 'NGN',
  status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'success', 'failed', 'abandoned', 'mismatch')),
  checkout_url     text,
  channel          text,                 -- card, bank_transfer, ...
  provider_txn     text,
  failure          text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  verified_at      timestamptz,
  created_by       text NOT NULL
);
CREATE INDEX billing_payments_pending ON billing_payments (created_at) WHERE status = 'pending';
CREATE INDEX billing_payments_invoice ON billing_payments (invoice_id);
ALTER TABLE billing_payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON billing_payments TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON billing_payments FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, UPDATE ON billing_payments TO taskiem_app;
GRANT SELECT (tenant_id, reference, provider, status, created_at) ON billing_payments TO taskiem_dispatch;

-- One row per provider event processed: a redelivered webhook is a no-op.
CREATE TABLE billing_webhook_receipts (
  provider     text NOT NULL,
  dedup_key    text NOT NULL,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  outcome      text NOT NULL,
  received_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (provider, dedup_key)
);
ALTER TABLE billing_webhook_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_webhook_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON billing_webhook_receipts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON billing_webhook_receipts TO taskiem_app;

-- +goose Down
DROP TABLE billing_webhook_receipts;
DROP TABLE billing_payments;
DROP TRIGGER invoices_immutable ON invoices;
DROP FUNCTION taskiem_invoice_immutable();
DROP TABLE invoices;
DROP FUNCTION taskiem_next_invoice_number(text, timestamptz);
DROP TABLE billing_invoice_counters;
