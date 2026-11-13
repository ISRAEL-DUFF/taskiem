-- A linked business as a supplier needs no phone number (13 November 2026,
-- the founder): its orders, its bills and what it says reach the buyer
-- through the link, never by SMS. Ordering from a linked business that was
-- not yet a supplier asked the buyer for the seller's number; the supplier
-- is now made from the link alone. One that is not linked still has its
-- number, which is how its orders reach it.
-- +goose Up
ALTER TABLE suppliers ALTER COLUMN phone_hash DROP NOT NULL;
ALTER TABLE suppliers ALTER COLUMN phone DROP NOT NULL;
ALTER TABLE suppliers ADD CONSTRAINT suppliers_reached CHECK ((phone_hash IS NOT NULL AND phone IS NOT NULL) OR link_id IS NOT NULL);

-- +goose Down
-- Suppliers made from a link alone go when nothing was ordered from them;
-- one with orders stops this going down (it has no number to give back).
ALTER TABLE suppliers DROP CONSTRAINT suppliers_reached;
DELETE FROM suppliers s WHERE s.phone_hash IS NULL AND NOT EXISTS (SELECT 1 FROM purchase_orders o WHERE o.supplier_id = s.id);
ALTER TABLE suppliers ALTER COLUMN phone_hash SET NOT NULL;
ALTER TABLE suppliers ALTER COLUMN phone SET NOT NULL;
