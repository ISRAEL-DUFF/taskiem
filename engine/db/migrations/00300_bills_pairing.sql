-- Business links, L1 review: pairing and paying (docs/spec/business-links.md
-- §5.3, §6.2, §16.2).
--
-- A bill's payment records when the rail took the money (sent_at: the
-- payout step answered pending or settled), the rail's payout id, and when
-- the buyer's owner was alerted about it. A payment whose run died after
-- the money went is never failed: it is unknown, the bill stays paying,
-- and the owner is told. The pairing definer answers only a payout the
-- calling seller was paid, sent, within three days of sending, and
-- audits each call on the buyer's side. The mirror trigger fires on the
-- mirrored columns alone, and the reconciler claims every open invoice of
-- a link, so a link accepted again refreshes what went stale while it
-- was ended.
-- +goose Up
ALTER TABLE bill_payments ADD COLUMN sent_at timestamptz;
ALTER TABLE bill_payments ADD COLUMN payout_ref text CHECK (length(payout_ref) <= 200);
ALTER TABLE bill_payments ADD COLUMN alerted_at timestamptz;
ALTER TABLE bill_payments DROP CONSTRAINT bill_payments_status_check;
ALTER TABLE bill_payments ADD CONSTRAINT bill_payments_status_check CHECK (status IN ('pending', 'paid', 'failed', 'rejected', 'unknown'));
GRANT SELECT (bill_id, sent_at, alerted_at) ON bill_payments TO taskiem_dispatch;
GRANT SELECT (supplier_tenant) ON bills TO taskiem_dispatch;
-- The buyer confirms a pair the seller's matching did not make (§5.3).
ALTER TABLE link_notices DROP CONSTRAINT link_notices_kind_check;
ALTER TABLE link_notices ADD CONSTRAINT link_notices_kind_check CHECK (kind IN ('declared', 'queried', 'resolved', 'paired'));

-- Pairing (§5.3): the token of the one unpaired payout of an amount the
-- linked buyer sent to the calling seller in the last three days, for a
-- credit into the buyer's account that carried no token. Nothing when
-- there is none, or more than one, or the payout is still waiting to be
-- approved or sent. Called by the seller's matching alone, once per
-- credit, with that credit's amount; it answers a token and nothing
-- else, and each call is audited in the buyer's chain (link.payout_token,
-- with the amount asked), so a seller cannot sweep amounts unseen.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_payout_token(p_customer text, p_amount bigint) RETURNS text
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_buyer uuid;
  v_link uuid;
  v_tokens text[];
BEGIN
  IF p_customer !~ '^key:link:[0-9a-f-]{36}$' OR p_amount <= 0 THEN
    RETURN NULL;
  END IF;
  v_buyer := substr(p_customer, 10)::uuid;
  SELECT l.id INTO v_link FROM business_links l
   WHERE l.a_tenant = LEAST(v_me, v_buyer) AND l.b_tenant = GREATEST(v_me, v_buyer) AND l.status = 'active';
  IF v_link IS NULL THEN
    RETURN NULL;
  END IF;
  SELECT array_agg(p.token) INTO v_tokens FROM bill_payments p
    JOIN bills b ON b.id = p.bill_id AND b.tenant_id = v_buyer AND b.supplier_tenant = v_me AND b.link_id = v_link
   WHERE p.tenant_id = v_buyer AND p.amount_kobo = p_amount AND p.status IN ('pending', 'paid', 'unknown') AND p.paired_at IS NULL
     AND p.sent_at IS NOT NULL AND p.sent_at > now() - interval '3 days';
  -- Audited in the buyer's chain, as the door audits the other side.
  PERFORM set_config('app.tenant_scope', '{' || v_buyer::text || '}', true);
  PERFORM taskiem_audit_append(v_buyer, 'system', 'link:' || v_me::text || '/matching', 'link.payout_token', v_link::text,
    jsonb_build_object('link', v_link, 'business', v_me, 'amount_kobo', p_amount, 'answered', cardinality(v_tokens) = 1));
  PERFORM set_config('app.tenant_scope', '{' || v_me::text || '}', true);
  IF cardinality(v_tokens) = 1 THEN
    RETURN v_tokens[1];
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_payout_token(text, bigint) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_payout_token(text, bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_payout_token(text, bigint) TO taskiem_app;

-- The businesses whose bill payments need looking at (the api's tick,
-- every few minutes): a payment still pending, or a payout sent three
-- days ago and still unpaired, its owner not yet told. Tenant ids only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_bill_payments_tenants() RETURNS SETOF uuid
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT p.tenant_id FROM bill_payments p
   WHERE p.status = 'pending'
      OR (p.status = 'paid' AND p.paired_at IS NULL AND p.alerted_at IS NULL AND p.sent_at IS NOT NULL AND p.sent_at < now() - interval '3 days')
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_bill_payments_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_bill_payments_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_bill_payments_tenants() TO taskiem_app;

-- The mirror trigger fires when a mirrored column changes (§16.2), not on
-- every change of a linked invoice (a payment moves the invoice by
-- touching updated_at, so the columns are compared, not the statement's
-- SET list).
DROP TRIGGER customer_invoice_link_mirror ON customer_invoices;
CREATE TRIGGER customer_invoice_link_mirror AFTER INSERT ON customer_invoices
  FOR EACH ROW WHEN (NEW.customer_key LIKE 'link:%' AND NEW.sent_at IS NOT NULL)
  EXECUTE FUNCTION taskiem_link_mirror_note();
CREATE TRIGGER customer_invoice_link_mirror_change AFTER UPDATE ON customer_invoices
  FOR EACH ROW WHEN (NEW.customer_key LIKE 'link:%' AND NEW.sent_at IS NOT NULL AND (
    OLD.number IS DISTINCT FROM NEW.number OR OLD.lines IS DISTINCT FROM NEW.lines OR OLD.total_kobo IS DISTINCT FROM NEW.total_kobo
    OR OLD.paid_kobo IS DISTINCT FROM NEW.paid_kobo OR OLD.due_on IS DISTINCT FROM NEW.due_on OR OLD.voided_at IS DISTINCT FROM NEW.voided_at
    OR OLD.disputed IS DISTINCT FROM NEW.disputed OR OLD.allow_part_payments IS DISTINCT FROM NEW.allow_part_payments
    OR OLD.sent_at IS DISTINCT FROM NEW.sent_at OR OLD.customer_key IS DISTINCT FROM NEW.customer_key))
  EXECUTE FUNCTION taskiem_link_mirror_note();

-- The reconciler compares every invoice that has a bill, and every open
-- invoice of the link, whenever it was sent: a link accepted again
-- refreshes the bills that went stale while it was ended. An invoice
-- settled before the link was accepted, with no bill, is still not
-- claimed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_reconcile() RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_n int;
BEGIN
  WITH sides AS (
    SELECT l.id AS link, l.answered_at, s.seller, s.buyer FROM business_links l
      CROSS JOIN LATERAL (VALUES (l.a_tenant, l.b_tenant), (l.b_tenant, l.a_tenant)) s(seller, buyer)
     WHERE l.status = 'active'
  ), wrong AS (
    SELECT i.tenant_id, i.id FROM sides x
      JOIN customer_invoices i ON i.tenant_id = x.seller AND i.customer_key = 'link:' || x.buyer::text AND i.sent_at IS NOT NULL
      LEFT JOIN bills b ON b.tenant_id = x.buyer AND b.link_id = x.link AND b.source_id = i.id
     WHERE (i.sent_at >= x.answered_at OR b.id IS NOT NULL OR (i.voided_at IS NULL AND i.paid_kobo < i.total_kobo))
       AND (b.id IS NULL OR b.reference <> i.number OR b.amount_kobo <> i.total_kobo OR b.paid_kobo <> i.paid_kobo
        OR b.due_on IS DISTINCT FROM i.due_on OR b.voided <> (i.voided_at IS NOT NULL))
  ), put AS (
    INSERT INTO link_mirrors_due (tenant_id, invoice_id) SELECT tenant_id, id FROM wrong
    ON CONFLICT (tenant_id, invoice_id) DO UPDATE SET due_at = now()
    RETURNING 1
  )
  SELECT count(*) INTO v_n FROM put;
  RETURN v_n;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_reconcile() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_reconcile() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_reconcile() TO taskiem_app;

-- +goose Down
-- The definers as 00292 made them.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_payout_token(p_customer text, p_amount bigint) RETURNS text
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_buyer uuid;
  v_tokens text[];
BEGIN
  IF p_customer !~ '^key:link:[0-9a-f-]{36}$' OR p_amount <= 0 THEN
    RETURN NULL;
  END IF;
  v_buyer := substr(p_customer, 10)::uuid;
  IF NOT EXISTS (SELECT 1 FROM business_links l WHERE l.a_tenant = LEAST(v_me, v_buyer) AND l.b_tenant = GREATEST(v_me, v_buyer) AND l.status = 'active') THEN
    RETURN NULL;
  END IF;
  SELECT array_agg(p.token) INTO v_tokens FROM bill_payments p
   WHERE p.tenant_id = v_buyer AND p.amount_kobo = p_amount AND p.status IN ('pending', 'paid') AND p.paired_at IS NULL
     AND p.created_at > now() - interval '3 days';
  IF cardinality(v_tokens) = 1 THEN
    RETURN v_tokens[1];
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_reconcile() RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_n int;
BEGIN
  WITH sides AS (
    SELECT l.id AS link, l.answered_at, s.seller, s.buyer FROM business_links l
      CROSS JOIN LATERAL (VALUES (l.a_tenant, l.b_tenant), (l.b_tenant, l.a_tenant)) s(seller, buyer)
     WHERE l.status = 'active'
  ), wrong AS (
    SELECT i.tenant_id, i.id FROM sides x
      JOIN customer_invoices i ON i.tenant_id = x.seller AND i.customer_key = 'link:' || x.buyer::text
                              AND i.sent_at IS NOT NULL AND i.sent_at >= x.answered_at
      LEFT JOIN bills b ON b.tenant_id = x.buyer AND b.link_id = x.link AND b.source_id = i.id
     WHERE b.id IS NULL OR b.reference <> i.number OR b.amount_kobo <> i.total_kobo OR b.paid_kobo <> i.paid_kobo
        OR b.due_on IS DISTINCT FROM i.due_on OR b.voided <> (i.voided_at IS NOT NULL)
  ), put AS (
    INSERT INTO link_mirrors_due (tenant_id, invoice_id) SELECT tenant_id, id FROM wrong
    ON CONFLICT (tenant_id, invoice_id) DO UPDATE SET due_at = now()
    RETURNING 1
  )
  SELECT count(*) INTO v_n FROM put;
  RETURN v_n;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_bill_payments_tenants();
DROP TRIGGER customer_invoice_link_mirror_change ON customer_invoices;
DROP TRIGGER customer_invoice_link_mirror ON customer_invoices;
CREATE TRIGGER customer_invoice_link_mirror AFTER INSERT OR UPDATE ON customer_invoices
  FOR EACH ROW WHEN (NEW.customer_key LIKE 'link:%' AND NEW.sent_at IS NOT NULL)
  EXECUTE FUNCTION taskiem_link_mirror_note();
REVOKE SELECT (supplier_tenant) ON bills FROM taskiem_dispatch;
REVOKE SELECT (bill_id, sent_at, alerted_at) ON bill_payments FROM taskiem_dispatch;
DELETE FROM link_notices WHERE kind = 'paired';
ALTER TABLE link_notices DROP CONSTRAINT link_notices_kind_check;
ALTER TABLE link_notices ADD CONSTRAINT link_notices_kind_check CHECK (kind IN ('declared', 'queried', 'resolved'));
UPDATE bill_payments SET status = 'pending' WHERE status = 'unknown';
ALTER TABLE bill_payments DROP CONSTRAINT bill_payments_status_check;
ALTER TABLE bill_payments ADD CONSTRAINT bill_payments_status_check CHECK (status IN ('pending', 'paid', 'failed', 'rejected'));
ALTER TABLE bill_payments DROP COLUMN alerted_at;
ALTER TABLE bill_payments DROP COLUMN payout_ref;
ALTER TABLE bill_payments DROP COLUMN sent_at;
