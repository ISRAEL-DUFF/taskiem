-- After locking (P4b-4 of docs/spec/chat-pay-people.md; decision 0074).
--
-- payroll_code_waits takes the two payment actions confirmed with
-- Payrolla's code: pay_now (Pay now before the pay date) and retry_failed
-- (Pay the failed ones again). Still never the code.
--
-- chat_history gains kind payslip: a payslip's card in the Taskiem
-- conversation of the employee it is for, its text Payrolla's id for the
-- payslip and the period (JSON), drawn live for that person only.
--
-- The outbox's payroll_run kind also carries, as reach_reference, a
-- payment's news (the shortfall, the morning warnings, who was paid) and
-- a payslip's notice (its id, the employee and the period): no figures.
-- +goose Up
ALTER TABLE payroll_code_waits DROP CONSTRAINT payroll_code_waits_action_check;
ALTER TABLE payroll_code_waits ADD CONSTRAINT payroll_code_waits_action_check CHECK (action IN ('approve', 'lock', 'pay_now', 'retry_failed'));
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary', 'chart', 'menu', 'receipt', 'paid_you', 'payroll_run', 'payslip'));

-- +goose Down
DELETE FROM chat_history WHERE kind = 'payslip';
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary', 'chart', 'menu', 'receipt', 'paid_you', 'payroll_run'));
DELETE FROM payroll_code_waits WHERE action IN ('pay_now', 'retry_failed');
ALTER TABLE payroll_code_waits DROP CONSTRAINT payroll_code_waits_action_check;
ALTER TABLE payroll_code_waits ADD CONSTRAINT payroll_code_waits_action_check CHECK (action IN ('approve', 'lock'));
