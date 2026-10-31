-- Business links, L6: automation (decision 0061; docs/spec/business-links.md
-- §11). A workflow starts on what happens between linked businesses: the
-- "link" trigger, {"type": "link", "config": {"event": …}}, one of
-- bill.received, bill.paid, order.received, order.delivered,
-- thread.message and link.accepted. Each event is queued in the business
-- it happens to, at the moment it happens (one row per workflow that
-- starts on it, once per event), and the link worker starts the runs
-- (kind 'start'). trigger.body carries the link, the business's name,
-- the document's reference and the amounts: never the other side's
-- contact details.
-- +goose Up
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd', 'storefront', 'form', 'email', 'invoice', 'link'));

CREATE TABLE link_starts (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  workflow_id  uuid NOT NULL,
  version      int NOT NULL,
  event        text NOT NULL CHECK (event IN ('bill.received', 'bill.paid', 'order.received', 'order.delivered', 'thread.message', 'link.accepted')),
  -- The event once: the document and what happened to it.
  dedup_key    text NOT NULL CHECK (length(dedup_key) BETWEEN 1 AND 200),
  body         jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object'),
  run_id       uuid,
  note         text CHECK (length(note) <= 300),
  created_at   timestamptz NOT NULL DEFAULT now(),
  done_at      timestamptz,
  UNIQUE (workflow_id, dedup_key)
);
CREATE INDEX link_starts_open ON link_starts (tenant_id) WHERE done_at IS NULL;
ALTER TABLE link_starts ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_starts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON link_starts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON link_starts TO taskiem_app;

ALTER TABLE link_orders_due DROP CONSTRAINT link_orders_due_kind_check;
ALTER TABLE link_orders_due ADD CONSTRAINT link_orders_due_kind_check CHECK (kind IN ('order', 'mirror', 'invoice', 'receipt', 'prices', 'credit', 'start'));

-- +goose Down
DELETE FROM link_orders_due WHERE kind = 'start';
ALTER TABLE link_orders_due DROP CONSTRAINT link_orders_due_kind_check;
ALTER TABLE link_orders_due ADD CONSTRAINT link_orders_due_kind_check CHECK (kind IN ('order', 'mirror', 'invoice', 'receipt', 'prices', 'credit'));
DROP TABLE link_starts;
DELETE FROM triggers WHERE type = 'link';
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd', 'storefront', 'form', 'email', 'invoice'));
