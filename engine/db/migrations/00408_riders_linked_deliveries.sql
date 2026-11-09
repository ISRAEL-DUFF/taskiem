-- Riders as participants, phase 1 (docs/spec/chat-riders.md, decision
-- 0065): a rider that is a business on Taskiem, linked to the business it
-- delivers for, receives each delivery as a linked order in its own
-- business, one card on both sides, and moves it from the card, its Orders
-- tool or its WhatsApp buttons alike.
--
-- The business's deliveries row stays the record of the delivery (the
-- hold, the window, the door, cash, proof); what is added is how a linked
-- rider receives and answers it. A rider with no business on Taskiem is
-- reached as before.
-- +goose Up

-- A rider that is a linked business: its deliveries go through the link.
ALTER TABLE riders ADD COLUMN link_id uuid REFERENCES business_links(id);
CREATE UNIQUE INDEX riders_link ON riders (tenant_id, link_id) WHERE retired_at IS NULL AND link_id IS NOT NULL;

-- The rider's order for a delivery, on the business's record of it.
ALTER TABLE deliveries ADD COLUMN link_order_id uuid;

-- The rider's side: a linked order whose source is the business's delivery
-- (not a purchase order), with what a delivery carries beyond its one
-- line: where to pick up, where it goes (the area; the rest sealed with
-- the order's customer while held), whether a photo is asked, whether the
-- customer pays at the door, when the offer runs out, and the steps'
-- times. The business's reference is any order's, not only P-…
ALTER TABLE link_orders
  ADD COLUMN delivery_id    uuid,
  ADD COLUMN pickup         text CHECK (pickup IS NULL OR length(pickup) <= 300),
  ADD COLUMN pickup_link    text CHECK (pickup_link IS NULL OR length(pickup_link) <= 300),
  ADD COLUMN drop_area      text CHECK (drop_area IS NULL OR length(drop_area) <= 80),
  ADD COLUMN proof_required boolean NOT NULL DEFAULT false,
  ADD COLUMN door_payment   boolean NOT NULL DEFAULT false,
  ADD COLUMN expires_at     timestamptz,
  ADD COLUMN picked_up_at   timestamptz,
  ADD COLUMN arrived_at     timestamptz;
ALTER TABLE link_orders DROP CONSTRAINT link_orders_purchase_reference_check;
ALTER TABLE link_orders ADD CONSTRAINT link_orders_purchase_reference_check CHECK (length(purchase_reference) BETWEEN 3 AND 40);
CREATE INDEX link_orders_delivery ON link_orders (delivery_id) WHERE delivery_id IS NOT NULL;

-- +goose Down
DROP INDEX link_orders_delivery;
DELETE FROM link_messages WHERE kind = 'card' AND card ->> 'doc' IN ('delivery', 'dispatch');
DELETE FROM link_orders WHERE delivery_id IS NOT NULL;
ALTER TABLE link_orders DROP CONSTRAINT link_orders_purchase_reference_check;
ALTER TABLE link_orders ADD CONSTRAINT link_orders_purchase_reference_check CHECK (purchase_reference ~ '^P-[A-Z0-9]{6}$');
ALTER TABLE link_orders DROP COLUMN arrived_at, DROP COLUMN picked_up_at, DROP COLUMN expires_at, DROP COLUMN door_payment, DROP COLUMN proof_required,
  DROP COLUMN drop_area, DROP COLUMN pickup_link, DROP COLUMN pickup, DROP COLUMN delivery_id;
ALTER TABLE deliveries DROP COLUMN link_order_id;
DROP INDEX riders_link;
ALTER TABLE riders DROP COLUMN link_id;
