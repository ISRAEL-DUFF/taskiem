-- A sale on credit (decision 0060; docs/spec/no-sheet-needed.md §6.4 and
-- Q3, R5): "sold 3 bags of rice 45,000 credit to Ada" lowers the stock as
-- any sale does and records what Ada owes as a notebook debt, an invoice
-- of kind credit with the sale's lines, in the sale's one transaction.
-- No money moves: the sale is paid_how credit, status credit, and names its
-- invoice. Ada's payments are counted towards that invoice as towards any
-- of hers, never towards the sale.
-- +goose Up
-- The status and paid_how checks named afresh: the old ones were unnamed.
-- +goose StatementBegin
DO $$
DECLARE c record;
BEGIN
  FOR c IN SELECT conname FROM pg_constraint WHERE conrelid = 'stock_sales'::regclass AND contype = 'c'
             AND (pg_get_constraintdef(oid) LIKE '%status%' OR pg_get_constraintdef(oid) LIKE '%paid_how%') LOOP
    EXECUTE format('ALTER TABLE stock_sales DROP CONSTRAINT %I', c.conname);
  END LOOP;
END
$$;
-- +goose StatementEnd
ALTER TABLE stock_sales ADD COLUMN invoice_id uuid REFERENCES customer_invoices(id),
  ADD CONSTRAINT stock_sales_paid_how CHECK (paid_how IN ('cash', 'transfer', 'pos', 'credit')),
  -- recorded: cash, its takings entry written; waiting: transfer or POS,
  -- its money not yet seen; matched: its money matched to it; credit: on
  -- credit, what the customer owes kept on its invoice.
  ADD CONSTRAINT stock_sales_status CHECK (status IN ('recorded', 'waiting', 'matched', 'credit')),
  ADD CONSTRAINT stock_sales_entry CHECK ((status IN ('waiting', 'credit')) = (entry_id IS NULL)),
  ADD CONSTRAINT stock_sales_matched CHECK ((status = 'matched') = (matched_how IS NOT NULL AND matched_at IS NOT NULL)),
  ADD CONSTRAINT stock_sales_recorded CHECK (status <> 'recorded' OR paid_how = 'cash'),
  ADD CONSTRAINT stock_sales_credit CHECK ((status = 'credit') = (paid_how = 'credit') AND (paid_how = 'credit') = (invoice_id IS NOT NULL));

-- +goose Down
DELETE FROM stock_sales WHERE paid_how = 'credit';
ALTER TABLE stock_sales DROP CONSTRAINT stock_sales_credit, DROP CONSTRAINT stock_sales_recorded, DROP CONSTRAINT stock_sales_matched,
  DROP CONSTRAINT stock_sales_entry, DROP CONSTRAINT stock_sales_status, DROP CONSTRAINT stock_sales_paid_how, DROP COLUMN invoice_id,
  ADD CHECK (paid_how IN ('cash', 'transfer', 'pos')),
  ADD CHECK (status IN ('recorded', 'waiting', 'matched')),
  ADD CHECK ((status = 'waiting') = (entry_id IS NULL)),
  ADD CHECK ((status = 'matched') = (matched_how IS NOT NULL AND matched_at IS NOT NULL)),
  ADD CHECK (status <> 'recorded' OR paid_how = 'cash');
