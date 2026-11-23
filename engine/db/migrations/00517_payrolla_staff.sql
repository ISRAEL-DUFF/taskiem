-- Payroll is Payrolla's (P4a): a staff member in Taskiem linked to the
-- employee they are in the business's Payrolla, so the Payroll page names
-- them and Payrolla's messages about their pay reach their own chat. An
-- owner confirms each link (suggested by the same email or phone). Only
-- Payrolla's id and the name it gave are kept: no pay, no bank details.
-- +goose Up
ALTER TABLE staff_profiles ADD COLUMN payrolla_employee_id text CHECK (length(payrolla_employee_id) <= 64),
  ADD COLUMN payrolla_name text CHECK (length(payrolla_name) <= 160),
  ADD COLUMN payrolla_linked_by text,
  ADD COLUMN payrolla_linked_at timestamptz;
CREATE UNIQUE INDEX staff_profiles_payrolla ON staff_profiles (tenant_id, payrolla_employee_id) WHERE payrolla_employee_id IS NOT NULL;

-- +goose Down
DROP INDEX staff_profiles_payrolla;
ALTER TABLE staff_profiles DROP COLUMN payrolla_linked_at, DROP COLUMN payrolla_linked_by, DROP COLUMN payrolla_name, DROP COLUMN payrolla_employee_id;
