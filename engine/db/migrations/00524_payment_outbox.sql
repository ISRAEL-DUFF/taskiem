-- How a payment started from the chat ended, told back in the chat
-- (docs/spec/chat-pay-people.md, "WhatsApp, Telegram and Slack"): kind
-- payment names the payment, kind payment_batch a batch prepared from
-- the chat (one message when its last row ends). The row names the
-- payment or the batch only; the words are made from it when sent.
-- +goose Up
ALTER TABLE whatsapp_outbox ADD COLUMN payment_id uuid, ADD COLUMN batch_id uuid;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening', 'supplier', 'balance', 'payment', 'payment_batch'));
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

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind IN ('payment', 'payment_batch');
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family', 'rider', 'supplier', 'balance')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider', 'supplier', 'balance') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind IN ('invoice', 'balance')) = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind IN ('money_daily', 'sales_evening')) = (digest_id IS NOT NULL)));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening', 'supplier', 'balance'));
ALTER TABLE whatsapp_outbox DROP COLUMN payment_id, DROP COLUMN batch_id;
