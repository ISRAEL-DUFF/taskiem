-- This month's run in Taskiem (P4b-3 of docs/spec/chat-pay-people.md;
-- decision 0074).
--
-- payroll_code_waits: a code Payrolla sent a person for one action on one
-- run (approve or lock), waiting to be typed into a WhatsApp or Telegram
-- thread: Payrolla's challenge id, the action, the run and its period, the
-- person, the channel and when it stops waiting (at most 10 minutes).
-- Never the code: a message of six digits while one waits is sent to
-- Payrolla once and kept nowhere. One waits per person, business and
-- channel; a new one replaces it, and "stop" or its end drops it.
--
-- whatsapp_outbox gains kind payroll_run: a run's card or what happened to
-- it (worked out, sent back with its note, decided in Payrolla, payment
-- scheduled), told to a person on the web conversation and their first
-- chat app. reach_reference carries the run's id, the event and the note
-- (JSON); figures are read from Payrolla when it is sent, never kept.
--
-- chat_history gains kind payroll_run: a run's card in the Taskiem
-- conversation, its text the run's id and period (JSON), drawn live.
-- +goose Up
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary', 'chart', 'menu', 'payroll_run'));
CREATE TABLE payroll_code_waits (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel      text NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'web')),
  challenge_id text NOT NULL CHECK (length(challenge_id) <= 64),
  action       text NOT NULL CHECK (action IN ('approve', 'lock')),
  run_id       text NOT NULL CHECK (length(run_id) <= 64),
  period       text NOT NULL DEFAULT '' CHECK (length(period) <= 40),
  expires_at   timestamptz NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id, channel)
);
CREATE INDEX payroll_code_waits_expires ON payroll_code_waits (expires_at);
ALTER TABLE payroll_code_waits ENABLE ROW LEVEL SECURITY;
ALTER TABLE payroll_code_waits FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payroll_code_waits TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON payroll_code_waits TO taskiem_app;

ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening', 'supplier', 'balance', 'payment', 'payment_batch', 'payroll_run'));
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_payroll_check CHECK (kind <> 'payroll_run' OR (user_id IS NOT NULL AND reach_reference IS NOT NULL));

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind = 'payroll_run';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_payroll_check;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening', 'supplier', 'balance', 'payment', 'payment_batch'));
DROP TABLE payroll_code_waits;
DELETE FROM chat_history WHERE kind = 'payroll_run';
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary', 'chart', 'menu'));
