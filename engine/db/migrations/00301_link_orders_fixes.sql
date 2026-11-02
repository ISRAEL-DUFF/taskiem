-- Business links, L2 to L5 review fixes (docs/spec/business-links.md
-- §16.3, §16.5, §16.6, §17).
--
-- The seller's stock for an accepted linked order is lowered after the
-- acceptance commits, and the buyer's stock is adjusted after a short
-- count the seller agreed: each is work on the link worker's list, so a
-- failure is tried again, never lost (kinds 'stock' and 'adjust').
--
-- The buyer's purchase order keeps its own count of a short delivery
-- (short: what came of each line, by the buyer), so Not all came after
-- the day is a dispute on the card, settled by the seller's agreement
-- with a stock adjustment; and notes when the seller raised the order's
-- invoice (invoiced_at, mirrored), so the buyer's statement counts its
-- accepted orders not yet invoiced as the seller's does.
-- +goose Up
ALTER TABLE link_orders_due DROP CONSTRAINT link_orders_due_kind_check;
ALTER TABLE link_orders_due ADD CONSTRAINT link_orders_due_kind_check CHECK (kind IN ('order', 'mirror', 'invoice', 'receipt', 'prices', 'credit', 'start', 'stock', 'adjust'));

ALTER TABLE purchase_orders ADD COLUMN short jsonb CHECK (short IS NULL OR jsonb_typeof(short) = 'array');
ALTER TABLE purchase_orders ADD COLUMN invoiced_at timestamptz;

-- +goose Down
ALTER TABLE purchase_orders DROP COLUMN invoiced_at;
ALTER TABLE purchase_orders DROP COLUMN short;
DELETE FROM link_orders_due WHERE kind IN ('stock', 'adjust');
ALTER TABLE link_orders_due DROP CONSTRAINT link_orders_due_kind_check;
ALTER TABLE link_orders_due ADD CONSTRAINT link_orders_due_kind_check CHECK (kind IN ('order', 'mirror', 'invoice', 'receipt', 'prices', 'credit', 'start'));
