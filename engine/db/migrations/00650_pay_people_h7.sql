-- Paying one person, finished (docs/spec/build-plan-gaps.md H7;
-- docs/spec/chat-pay-people.md "After" and Settled rows 2, 4, 6 and 7).
--
-- - A payment keeps the fee its provider quoted before it was confirmed
--   (fee_quote_kobo; none when the provider quotes none, and the fee then
--   shows once paid, fee_kobo), and the route Taskiem chose: a bank
--   transfer, or Taskiem wallet to Taskiem wallet when the account paid is
--   another business's Taskiem wallet (route 'wallet', to_wallet its
--   wallet's id, never shown), falling back to the bank if that fails
--   (route then 'bank' again, said by the run's finish).
-- - Whether the person paid is told in their chat once the provider
--   confirms (tell_payee, on by default; Settled 6), whether the payer
--   asked for a receipt to send them (send_receipt), and where the diary
--   line of it is: the payer's Taskiem conversation ('taskiem') or their
--   side of a conversation with a linked business (its link's id).
-- - A paid payment the bank later sent back is 'reversed'.
-- - The diary lines and the payee's card name their payment (or batch),
--   so a line shows the payment as it stands: paid, failed with Try
--   again, received by the bank.
-- - pay_settings.shape: which version of the pay-person workflow a
--   business's was made from, so an older one is made again.
-- - taskiem_wallet_for_account: whether an account number is another
--   business's Taskiem wallet, and that wallet's id only (for paying it
--   wallet to wallet), never whose it is.
-- +goose Up
ALTER TABLE payments ADD COLUMN fee_quote_kobo bigint CHECK (fee_quote_kobo >= 0),
  ADD COLUMN route text NOT NULL DEFAULT 'bank' CHECK (route IN ('bank', 'wallet')),
  ADD COLUMN to_wallet text CHECK (length(to_wallet) <= 255),
  ADD COLUMN tell_payee boolean NOT NULL DEFAULT true,
  ADD COLUMN send_receipt boolean NOT NULL DEFAULT false,
  ADD COLUMN diary_where text CHECK (diary_where = 'taskiem' OR diary_where ~ '^[0-9a-f-]{36}$'),
  ADD COLUMN payee_told text CHECK (payee_told IN ('paid', 'failed', 'reversed'));
ALTER TABLE payments DROP CONSTRAINT payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check
  CHECK (status IN ('prepared', 'sending', 'waiting_ok', 'paid', 'failed', 'rejected', 'unknown', 'cancelled', 'reversed'));
CREATE INDEX payments_payout ON payments (tenant_id, provider_channel, payout_ref) WHERE payout_ref IS NOT NULL;
CREATE INDEX payments_run ON payments (run_id) WHERE run_id IS NOT NULL;

ALTER TABLE payment_batches ADD COLUMN diary_where text CHECK (diary_where = 'taskiem' OR diary_where ~ '^[0-9a-f-]{36}$');

ALTER TABLE pay_settings ADD COLUMN shape integer NOT NULL DEFAULT 0;

ALTER TABLE chat_history ADD COLUMN payment_id uuid, ADD COLUMN batch_id uuid;
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check
  CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary', 'chart', 'menu', 'receipt', 'paid_you'));
CREATE INDEX chat_history_payment ON chat_history (payment_id) WHERE payment_id IS NOT NULL;

ALTER TABLE link_messages ADD COLUMN payment_id uuid, ADD COLUMN batch_id uuid;

-- A reply of "pay" quoting a message with account details in it, on a chat
-- app that sends only the quoted message's id (WhatsApp, Slack): the
-- message, sealed, kept a week under its id, for its person only.
CREATE TABLE chat_pay_quotes (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  channel     text NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'slack')),
  message_id  text NOT NULL CHECK (length(message_id) BETWEEN 1 AND 200),
  body        jsonb NOT NULL CHECK (body ? '$pii'),
  expires_at  timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, user_id, channel, message_id)
);
CREATE INDEX chat_pay_quotes_expiry ON chat_pay_quotes (expires_at);
ALTER TABLE chat_pay_quotes ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_pay_quotes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON chat_pay_quotes TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON chat_pay_quotes TO taskiem_app;

-- Another business's Taskiem wallet by its account number: its wallet id,
-- for a transfer between wallets, and nothing else (not whose it is, its
-- name or balance). Only a live, prod wallet that is not the asker's own.
-- +goose StatementBegin
CREATE FUNCTION taskiem_wallet_for_account(p_tenant uuid, p_account text)
RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT m.wallet_id FROM receive_money m JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
   WHERE p_account ~ '^[0-9]{10}$' AND m.account_number = p_account AND m.environment = 'prod'
     AND m.tenant_id <> p_tenant
     AND m.mode = (SELECT o.mode FROM receive_money o WHERE o.tenant_id = p_tenant)
   LIMIT 1
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_wallet_for_account(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_wallet_for_account(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_wallet_for_account(uuid, text) TO taskiem_app;
CREATE POLICY dispatch_wallet_account ON receive_money FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, environment, mode, wallet_id, account_number) ON receive_money TO taskiem_dispatch;

-- The word to the person paid (kind payee_paid): a staff member by their
-- user, a rider found by their phone when it is sent (neither user nor
-- contact on the row); it names the payment.
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening', 'supplier', 'balance', 'payment', 'payment_batch', 'payee_paid'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind = 'payee_paid' OR (kind IN ('message', 'invoice', 'school_family', 'rider', 'supplier', 'balance')) = (user_id IS NULL))
     AND (kind <> 'payee_paid' OR contact_id IS NULL AND reach_reference IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider', 'supplier', 'balance') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind IN ('invoice', 'balance')) = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind IN ('money_daily', 'sales_evening')) = (digest_id IS NOT NULL))
     AND ((kind IN ('payment', 'payee_paid')) = (payment_id IS NOT NULL))
     AND ((kind = 'payment_batch') = (batch_id IS NOT NULL)));

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind = 'payee_paid';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family', 'rider', 'supplier', 'balance')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider', 'supplier', 'balance') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind IN ('invoice', 'balance')) = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind IN ('money_daily', 'sales_evening')) = (digest_id IS NOT NULL))
     AND ((kind = 'payment') = (payment_id IS NOT NULL))
     AND ((kind = 'payment_batch') = (batch_id IS NOT NULL)));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening', 'supplier', 'balance', 'payment', 'payment_batch'));
REVOKE SELECT (tenant_id, environment, mode, wallet_id, account_number) ON receive_money FROM taskiem_dispatch;
DROP POLICY dispatch_wallet_account ON receive_money;
DROP FUNCTION taskiem_wallet_for_account(uuid, text);
DROP TABLE chat_pay_quotes;
ALTER TABLE link_messages DROP COLUMN payment_id, DROP COLUMN batch_id;
DELETE FROM chat_history WHERE kind IN ('receipt', 'paid_you');
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary', 'chart', 'menu'));
DROP INDEX chat_history_payment;
ALTER TABLE chat_history DROP COLUMN payment_id, DROP COLUMN batch_id;
ALTER TABLE pay_settings DROP COLUMN shape;
ALTER TABLE payment_batches DROP COLUMN diary_where;
DROP INDEX payments_run;
DROP INDEX payments_payout;
UPDATE payments SET status = 'paid' WHERE status = 'reversed';
ALTER TABLE payments DROP CONSTRAINT payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check
  CHECK (status IN ('prepared', 'sending', 'waiting_ok', 'paid', 'failed', 'rejected', 'unknown', 'cancelled'));
ALTER TABLE payments DROP COLUMN fee_quote_kobo, DROP COLUMN route, DROP COLUMN to_wallet, DROP COLUMN tell_payee,
  DROP COLUMN send_receipt, DROP COLUMN diary_where, DROP COLUMN payee_told;
