-- Invoices on WhatsApp and Telegram (decision 0045 point 7, T4;
-- docs/invoices.md): when an invoice is sent to a customer who asked the
-- business for updates on a chat app (decision 0039), the chat outbox
-- carries it to that contact's channel with its buttons. The row names the
-- invoice and the contact; the message is rendered at each attempt, so no
-- text, number or link is stored.
-- +goose Up
ALTER TABLE whatsapp_outbox ADD COLUMN invoice_id uuid;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind <> 'invoice' OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL)));

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind = 'invoice';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message'));
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind = 'message') = (user_id IS NULL) AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL)));
ALTER TABLE whatsapp_outbox DROP COLUMN invoice_id;
