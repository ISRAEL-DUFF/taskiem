-- What a customer owes in all, on the chat app they asked the business for
-- updates on (decision 0060, R5; invoicing.OutboxBalance): `send Ada her
-- balance` and a reminder per customer. The row names the contact and one
-- open invoice of theirs, as an invoice's does; the message is worked out
-- when it is sent.
-- +goose Up
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening', 'supplier', 'balance'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family', 'rider', 'supplier', 'balance')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider', 'supplier', 'balance') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind IN ('invoice', 'balance')) = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind IN ('money_daily', 'sales_evening')) = (digest_id IS NOT NULL)));

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind = 'balance';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match',
  'money_daily', 'school_approval', 'school_family', 'rider', 'sales_evening', 'supplier'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice', 'school_family', 'rider', 'supplier')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind NOT IN ('invoice', 'school_family', 'rider', 'supplier') OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind IN ('money_daily', 'sales_evening')) = (digest_id IS NOT NULL)));
