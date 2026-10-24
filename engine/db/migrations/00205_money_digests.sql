-- Money summaries sent on a schedule (decision 0045; docs/spec/
-- invoices-and-money-log.md 9.2, 9.3, T7; docs/money-reports.md): every
-- morning yesterday's money to each owner on the chat they linked, and on
-- the 1st last month's by email. One row per business, kind and period,
-- taken before anything is sent, so restarts and several schedulers send
-- each summary once. The chat message is an outbox row naming the digest
-- and the owner; its text is rendered at each attempt from the money log.
-- +goose Up
CREATE TABLE money_digests (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  kind        text NOT NULL CHECK (kind IN ('daily', 'monthly')),
  -- The Lagos day, or the first day of the Lagos month.
  period      date NOT NULL,
  -- sent: queued to the owners' chats, or emailed; quiet: nothing moved;
  -- no_one: no owner could be reached.
  outcome     text NOT NULL CHECK (outcome IN ('sent', 'quiet', 'no_one')),
  sent_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, kind, period)
);
ALTER TABLE money_digests ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_digests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_digests TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON money_digests TO taskiem_app;

ALTER TABLE whatsapp_outbox ADD COLUMN digest_id uuid;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match', 'money_daily'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind <> 'invoice' OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL))
     AND ((kind = 'money_daily') = (digest_id IS NOT NULL)));

-- +goose Down
DELETE FROM whatsapp_outbox WHERE kind = 'money_daily';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message', 'invoice', 'match'));
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind IN ('message', 'invoice')) = (user_id IS NULL)
     AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL))
     AND (kind <> 'invoice' OR (contact_id IS NOT NULL AND reach_reference IS NULL))
     AND ((kind = 'invoice') = (invoice_id IS NOT NULL))
     AND ((kind = 'match') = (entry_id IS NOT NULL)));
ALTER TABLE whatsapp_outbox DROP COLUMN digest_id;
DROP TABLE money_digests;
