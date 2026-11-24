-- Sizes and colours (docs/spec/chat-categories-and-variants.md, Part B):
-- a product's stock shared out among its sizes is moved, out of the one and
-- into the others, not lost from the product and received by its sizes.

-- +goose Up
ALTER TABLE stock_movements DROP CONSTRAINT stock_movements_reason_check;
ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_reason_check
  CHECK (reason IN ('order', 'sale', 'received', 'adjustment', 'count', 'outside', 'moved'));

-- +goose Down
DELETE FROM stock_movements WHERE reason = 'moved';
ALTER TABLE stock_movements DROP CONSTRAINT stock_movements_reason_check;
ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_reason_check
  CHECK (reason IN ('order', 'sale', 'received', 'adjustment', 'count', 'outside'));
