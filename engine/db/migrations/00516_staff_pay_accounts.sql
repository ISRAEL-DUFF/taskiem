-- A staff member's account for payments outside payroll (payroll is
-- Payrolla's): one of the business's saved bank accounts, linked to the
-- staff member by an owner with a passkey, so Pay on their page knows
-- where the money goes; no account number is kept here. A payment records
-- the staff member it paid, so an account changed since they were last
-- paid is said, as a rider's is.
-- +goose Up
ALTER TABLE staff_profiles ADD COLUMN saved_id uuid REFERENCES saved_objects(id) ON DELETE SET NULL,
  ADD COLUMN saved_linked_by text,
  ADD COLUMN saved_linked_at timestamptz;
ALTER TABLE payments ADD COLUMN staff_id uuid;
CREATE INDEX payments_staff ON payments (tenant_id, staff_id, created_at DESC) WHERE staff_id IS NOT NULL;

-- +goose Down
DROP INDEX payments_staff;
ALTER TABLE payments DROP COLUMN staff_id;
ALTER TABLE staff_profiles DROP COLUMN saved_linked_at, DROP COLUMN saved_linked_by, DROP COLUMN saved_id;
