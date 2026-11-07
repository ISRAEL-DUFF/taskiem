-- Per-table readers (decision 0065; docs/data-tables.md#who-may-read-a-table).
--
-- A data table may name the membership roles that may read and change
-- its rows: readers. NULL, the default, is every table.read holder, as
-- before; a list restricts the rows to members holding one of its roles,
-- and the owner, on top of table.read (and table.edit to change them).
-- The rows live in Xenibase: the API and chat check readers before they
-- reach a row (engine/datatables.MayRead). An empty list would mean
-- nobody, so it is refused here as well as by the API. Workflows reach
-- rows through the data_tables connection as before; readers do not
-- limit them.
--
-- The app role may set readers when it makes a table and change them
-- later (the API asks for role.manage). RLS is unchanged: the column is
-- the tenant's like the rest of the row. taskiem_dispatch's table-wide
-- SELECT (00201) covers the column, listed in dispatch_columns.txt.
-- +goose Up
ALTER TABLE data_tables ADD COLUMN readers text[]
  CHECK (readers IS NULL OR (cardinality(readers) BETWEEN 1 AND 20 AND array_position(readers, NULL) IS NULL));

GRANT INSERT (readers), UPDATE (readers) ON data_tables TO taskiem_app;

-- +goose Down
REVOKE INSERT (readers), UPDATE (readers) ON data_tables FROM taskiem_app;
ALTER TABLE data_tables DROP COLUMN readers;
