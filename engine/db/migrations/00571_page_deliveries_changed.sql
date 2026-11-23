-- Pages (docs/spec/pages.md §5, §9): deliveries as a record view, kept up
-- to date. A delivery says when it last changed in changed_at, a column of
-- its own kept by the trigger orders and invoices have (00555,
-- taskiem_touch_changed_at), so a Confirm pressed on the web shows on the
-- phone without a reload. Added without a default (no rewrite of a large
-- table); the default given after applies to new rows, and rows older than
-- this migration are NULL until they change. Its index is built
-- concurrently in 00572.
-- +goose Up
ALTER TABLE deliveries ADD COLUMN changed_at timestamptz;
ALTER TABLE deliveries ALTER COLUMN changed_at SET DEFAULT clock_timestamp();
CREATE TRIGGER deliveries_changed BEFORE UPDATE ON deliveries
  FOR EACH ROW EXECUTE FUNCTION taskiem_touch_changed_at();

-- +goose Down
DROP TRIGGER deliveries_changed ON deliveries;
ALTER TABLE deliveries DROP COLUMN changed_at;
