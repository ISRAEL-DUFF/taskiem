-- Business links, L5: credit terms and limits between linked businesses
-- (decision 0061; docs/spec/business-links.md §10). The seller sets, per
-- link, the days a linked buyer has to pay (the due date of each invoice
-- to it, unless the invoice names one) and a credit limit: an order that
-- would take the buyer's open balance past it reaches the seller flagged
-- (not accepted by itself; the seller may still accept it, the owner's
-- decision), and the buyer is told, with Pay now; the flag clears when the
-- balance is paid down. The buyer sees its terms and limit as a mirror,
-- written through the door when the seller sets them. The statement each
-- side sees is computed from the invoices and the bills: nothing new is
-- kept for it.
-- +goose Up
ALTER TABLE trade_settings ADD COLUMN terms_days int CHECK (terms_days BETWEEN 0 AND 365);
ALTER TABLE trade_settings ADD COLUMN credit_limit_kobo bigint CHECK (credit_limit_kobo > 0 AND credit_limit_kobo <= 100000000000);

-- An order that would take the buyer past its limit: when it was flagged.
ALTER TABLE link_orders ADD COLUMN over_limit_at timestamptz;

-- The buyer's mirror of the terms its linked supplier gives it.
CREATE TABLE supplier_terms (
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  link_id            uuid NOT NULL REFERENCES business_links(id),
  terms_days         int,
  credit_limit_kobo  bigint,
  updated_at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, link_id)
);
ALTER TABLE supplier_terms ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_terms FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON supplier_terms TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON supplier_terms TO taskiem_app;

-- An order over the limit is checked again as the balance goes down.
ALTER TABLE link_orders_due DROP CONSTRAINT link_orders_due_kind_check;
ALTER TABLE link_orders_due ADD CONSTRAINT link_orders_due_kind_check CHECK (kind IN ('order', 'mirror', 'invoice', 'receipt', 'prices', 'credit'));

-- +goose Down
DELETE FROM link_orders_due WHERE kind = 'credit';
ALTER TABLE link_orders_due DROP CONSTRAINT link_orders_due_kind_check;
ALTER TABLE link_orders_due ADD CONSTRAINT link_orders_due_kind_check CHECK (kind IN ('order', 'mirror', 'invoice', 'receipt', 'prices'));
DROP TABLE supplier_terms;
ALTER TABLE link_orders DROP COLUMN over_limit_at;
ALTER TABLE trade_settings DROP COLUMN credit_limit_kobo;
ALTER TABLE trade_settings DROP COLUMN terms_days;
