-- Paying a rider (paying people, phase 2): the payment records the rider,
-- so an account changed since the rider was last paid is said, as a saved
-- account's is, and needs the owner's OK from staff.
-- +goose Up
ALTER TABLE payments ADD COLUMN rider_id uuid;
CREATE INDEX payments_rider ON payments (tenant_id, rider_id, created_at DESC) WHERE rider_id IS NOT NULL;

-- +goose Down
DROP INDEX payments_rider;
ALTER TABLE payments DROP COLUMN rider_id;
