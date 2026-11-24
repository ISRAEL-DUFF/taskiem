-- Pages G1, kept up to date (docs/spec/pages.md §9; decision 0072): an open
-- page is told when what it reads changes.
--
-- What changed is asked, as the Chat page's stream asks (00306), from an
-- index: an open page's live connection asks every two seconds which of its
-- sources changed since it last looked (api/pages_live.go). No NOTIFY on the
-- database that carries the runs.
--
-- Orders and invoices say when a row last changed in changed_at, a column
-- of its own kept by a trigger on every update, at the statement's clock,
-- so a change that commits a little later is read again within the
-- connection's look-back. updated_at is left as it was: what each writer
-- sets, which the shop's retention (00385) and shares (engine/storefront)
-- read, so taking a person's details off an ended order does not restart
-- its retention. changed_at is added without a default (no rewrite of a
-- large table); the default given after applies to new rows, and rows
-- older than this migration are NULL until they change.
--
-- A data table's rows are kept by Xenibase, not here: table_row_changes is
-- when Taskiem last wrote a table's rows (the API, the chat's tools, a
-- workflow's step, the shop's stock), one row a table, written after the
-- write in a short transaction of its own.
--
-- The indexes on the large tables are built concurrently, in 00565 to
-- 00567.
-- +goose Up
ALTER TABLE storefront_orders ADD COLUMN changed_at timestamptz;
ALTER TABLE storefront_orders ALTER COLUMN changed_at SET DEFAULT clock_timestamp();
ALTER TABLE customer_invoices ADD COLUMN changed_at timestamptz;
ALTER TABLE customer_invoices ALTER COLUMN changed_at SET DEFAULT clock_timestamp();

-- +goose StatementBegin
CREATE FUNCTION taskiem_touch_changed_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.changed_at := clock_timestamp();
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER storefront_orders_changed BEFORE UPDATE ON storefront_orders
  FOR EACH ROW EXECUTE FUNCTION taskiem_touch_changed_at();
CREATE TRIGGER customer_invoices_changed BEFORE UPDATE ON customer_invoices
  FOR EACH ROW EXECUTE FUNCTION taskiem_touch_changed_at();

CREATE TABLE table_row_changes (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  table_name  text NOT NULL,
  changed_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, table_name)
);
ALTER TABLE table_row_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE table_row_changes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON table_row_changes TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON table_row_changes TO taskiem_app;

-- +goose Down
DROP TABLE table_row_changes;
DROP TRIGGER customer_invoices_changed ON customer_invoices;
DROP TRIGGER storefront_orders_changed ON storefront_orders;
DROP FUNCTION taskiem_touch_changed_at();
ALTER TABLE customer_invoices DROP COLUMN changed_at;
ALTER TABLE storefront_orders DROP COLUMN changed_at;
