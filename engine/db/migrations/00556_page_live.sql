-- Pages G1, kept up to date (docs/spec/pages.md §9; decision 0072): an open
-- page is told when what it reads changes.
--
-- What changed is asked, as the Chat page's stream asks (00306), from an
-- index: an open page's live connection asks every two seconds which of its
-- sources changed since it last looked (api/pages_live.go). No NOTIFY on the
-- database that carries the runs.
--
-- Orders and invoices say when a row last changed: updated_at, now kept by
-- a trigger on every update (not every writer set it), at the statement's
-- clock, so a change that commits a little later is read again within the
-- connection's look-back.
--
-- A data table's rows are kept by Xenibase, not here: table_row_changes is
-- when Taskiem last wrote a table's rows (the API, the chat's tools, a
-- workflow's step), one row a table, written after the write in a short
-- transaction of its own.
-- +goose Up
ALTER TABLE storefront_orders ALTER COLUMN updated_at SET DEFAULT clock_timestamp();
ALTER TABLE customer_invoices ALTER COLUMN updated_at SET DEFAULT clock_timestamp();

-- +goose StatementBegin
CREATE FUNCTION taskiem_touch_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER storefront_orders_touch BEFORE UPDATE ON storefront_orders
  FOR EACH ROW EXECUTE FUNCTION taskiem_touch_updated_at();
CREATE TRIGGER customer_invoices_touch BEFORE UPDATE ON customer_invoices
  FOR EACH ROW EXECUTE FUNCTION taskiem_touch_updated_at();

CREATE INDEX storefront_orders_changed ON storefront_orders (tenant_id, updated_at);
CREATE INDEX customer_invoices_changed ON customer_invoices (tenant_id, updated_at);

CREATE TABLE table_row_changes (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  table_name  text NOT NULL,
  changed_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, table_name)
);
CREATE INDEX table_row_changes_changed ON table_row_changes (tenant_id, changed_at);
ALTER TABLE table_row_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE table_row_changes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON table_row_changes TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON table_row_changes TO taskiem_app;

-- +goose Down
DROP TABLE table_row_changes;
DROP INDEX customer_invoices_changed;
DROP INDEX storefront_orders_changed;
DROP TRIGGER customer_invoices_touch ON customer_invoices;
DROP TRIGGER storefront_orders_touch ON storefront_orders;
DROP FUNCTION taskiem_touch_updated_at();
ALTER TABLE customer_invoices ALTER COLUMN updated_at SET DEFAULT now();
ALTER TABLE storefront_orders ALTER COLUMN updated_at SET DEFAULT now();
