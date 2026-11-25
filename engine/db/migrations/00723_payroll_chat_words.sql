-- The chat's words for staff and this month's changes (docs/spec/
-- chat-pay-people.md, "As built: chat words"; decision 0074).
--
-- payroll_code_waits takes bank_change: a code Payrolla sent an approver
-- to approve one held salary account change, waiting to be typed into a
-- WhatsApp or Telegram thread. run_id holds Payrolla's id for the change
-- and period the employee's name (at most 40 characters). Still never the
-- code: six digits typed while it waits go to Payrolla once and are kept
-- nowhere.
-- +goose Up
ALTER TABLE payroll_code_waits DROP CONSTRAINT payroll_code_waits_action_check;
ALTER TABLE payroll_code_waits ADD CONSTRAINT payroll_code_waits_action_check CHECK (action IN ('approve', 'lock', 'pay_now', 'retry_failed', 'bank_change'));

-- +goose Down
DELETE FROM payroll_code_waits WHERE action = 'bank_change';
ALTER TABLE payroll_code_waits DROP CONSTRAINT payroll_code_waits_action_check;
ALTER TABLE payroll_code_waits ADD CONSTRAINT payroll_code_waits_action_check CHECK (action IN ('approve', 'lock', 'pay_now', 'retry_failed'));
