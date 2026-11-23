-- Purchases on the server, review fixes to U1 (docs/spec/what-a-business-
-- buys.md §5, §6, §11 As built).
--
-- - purchase_receipts: the keys of an order's receipts and short
--   adjustments, one row each, written in the receipt's own transaction:
--   the same key again inserts nothing, and the receipt is not made twice.
--   In place of looking for the key in audit_log, which no index covers,
--   under the order's row lock.
-- - purchase_lines.name_key: the line's name as the engine keys it (Go's
--   NameKey: spaces trimmed and single, Unicode spaces too, lowered),
--   written with the line, so the memory of a supplier's item by its name
--   (supplier_items.name_key, the same key) and Taskiem's suggestion find
--   it one way, not by a SQL copy of the key that differs on a non-breaking
--   space or the database's locale.
-- - supplier_items.name_key: its CHECK says what both keys hold (trimmed,
--   single ASCII spaces) and no more, so the engine's key always passes.
-- - purchase_lines.to_confirm: a line the business remembers how to treat
--   whose remembered treatment needs a yes it could not give (a supplier
--   placed the order: keeping track of more than 50 asks first): it is
--   recorded meanwhile, and asked like a line with no memory.
-- +goose Up
CREATE TABLE purchase_receipts (
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  purchase_order_id  uuid NOT NULL,
  kind               text NOT NULL CHECK (kind IN ('received', 'short_adjusted')),
  key                text NOT NULL CHECK (length(key) BETWEEN 1 AND 64),
  created_at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, purchase_order_id, kind, key),
  FOREIGN KEY (tenant_id, purchase_order_id) REFERENCES purchase_orders (tenant_id, id)
);
ALTER TABLE purchase_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_receipts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON purchase_receipts TO taskiem_app;
-- Receipts made before keep their keys.
INSERT INTO purchase_receipts (tenant_id, purchase_order_id, kind, key, created_at)
SELECT DISTINCT ON (a.tenant_id, o.id, a.action, a.detail->>'key') a.tenant_id, o.id, substr(a.action, 10), a.detail->>'key', a.at
  FROM audit_log a JOIN purchase_orders o ON o.tenant_id = a.tenant_id AND o.reference = a.target
 WHERE a.action IN ('purchase.received', 'purchase.short_adjusted') AND length(a.detail->>'key') BETWEEN 1 AND 64
ON CONFLICT DO NOTHING;

ALTER TABLE purchase_lines ADD COLUMN name_key text;
UPDATE purchase_lines SET name_key = lower(btrim(regexp_replace(name, '\s+', ' ', 'g')));
ALTER TABLE purchase_lines ALTER COLUMN name_key SET NOT NULL;
-- The engine writes every line's key. A line written without one (by hand)
-- is keyed near enough in SQL, as the lines before were.
-- +goose StatementBegin
CREATE FUNCTION taskiem_purchase_line_name_key() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.name_key IS NULL THEN
    NEW.name_key := lower(btrim(regexp_replace(NEW.name, '\s+', ' ', 'g')));
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER purchase_line_name_key BEFORE INSERT ON purchase_lines
  FOR EACH ROW EXECUTE FUNCTION taskiem_purchase_line_name_key();
ALTER TABLE purchase_lines ADD CONSTRAINT purchase_lines_name_key_form
  CHECK (length(name_key) BETWEEN 1 AND 200 AND name_key = btrim(name_key, ' ') AND strpos(name_key, '  ') = 0);
CREATE INDEX purchase_lines_name_key ON purchase_lines (tenant_id, name_key);

ALTER TABLE purchase_lines ADD COLUMN to_confirm boolean NOT NULL DEFAULT false;
GRANT UPDATE (to_confirm) ON purchase_lines TO taskiem_app;

ALTER TABLE supplier_items DROP CONSTRAINT supplier_items_name_key_check;
ALTER TABLE supplier_items ADD CONSTRAINT supplier_items_name_key_form
  CHECK (length(name_key) BETWEEN 1 AND 200 AND name_key = btrim(name_key, ' ') AND strpos(name_key, '  ') = 0);

-- +goose Down
ALTER TABLE supplier_items DROP CONSTRAINT supplier_items_name_key_form;
-- Keys the engine wrote may not be in the old form: kept, not checked.
ALTER TABLE supplier_items ADD CONSTRAINT supplier_items_name_key_check CHECK (length(name_key) BETWEEN 1 AND 200
  AND name_key = lower(regexp_replace(btrim(name_key), '\s+', ' ', 'g'))) NOT VALID;
REVOKE UPDATE (to_confirm) ON purchase_lines FROM taskiem_app;
ALTER TABLE purchase_lines DROP COLUMN to_confirm;
DROP INDEX purchase_lines_name_key;
DROP TRIGGER purchase_line_name_key ON purchase_lines;
DROP FUNCTION taskiem_purchase_line_name_key();
ALTER TABLE purchase_lines DROP COLUMN name_key;
DROP TABLE purchase_receipts;
