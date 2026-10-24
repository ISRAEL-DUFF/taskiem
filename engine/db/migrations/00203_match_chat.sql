-- Matching in chat (decision 0045, T5; docs/whatsapp.md#matching-payments):
-- when a payment gets suggestions and is not matched on its own, the
-- business's members who manage invoices are asked on WhatsApp or
-- Telegram, with Yes, match and No. The outbox row names the money-in
-- entry and the member; the message, with the entry's best open
-- suggestion, is rendered at each attempt, so no text, number or button
-- is stored.
-- +goose Up
ALTER TABLE whatsapp_outbox ADD COLUMN entry_id uuid;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind <> 'invoice' OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL)));

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind = 'match';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice'));
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind <> 'invoice' OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL)));
ALTER TABLE whatsapp_outbox DROP COLUMN entry_id;
