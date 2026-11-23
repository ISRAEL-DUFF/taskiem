-- Collecting people to pay from several messages (paying people, P3):
-- "Add to the list" on a message keeps a row on the person's own list,
-- across every chat, until it is paid as a batch or cleared. A row is a
-- saved account, or an account number (sealed, as a payment's is) with
-- its bank, an amount and what it is for, and the message it came from
-- (once each). Each person sees only their own list.
-- +goose Up
CREATE TABLE pay_collected (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  user_id        uuid NOT NULL,
  saved_id       uuid REFERENCES saved_objects(id) ON DELETE CASCADE,
  account_sealed jsonb,
  bank_name      text NOT NULL DEFAULT '' CHECK (length(bank_name) <= 80),
  last4          text NOT NULL DEFAULT '' CHECK (length(last4) <= 4),
  title          text NOT NULL DEFAULT '' CHECK (length(title) <= 120),
  name           text NOT NULL DEFAULT '' CHECK (length(name) <= 120),
  amount_kobo    bigint CHECK (amount_kobo IS NULL OR amount_kobo > 0),
  purpose        text NOT NULL DEFAULT '' CHECK (length(purpose) <= 40),
  note           text NOT NULL DEFAULT '' CHECK (length(note) <= 200),
  -- The message it came from: its chat's kind and id, and the message.
  source_kind    text NOT NULL DEFAULT '' CHECK (length(source_kind) <= 20),
  source_chat    text NOT NULL DEFAULT '' CHECK (length(source_chat) <= 80),
  source_message text NOT NULL DEFAULT '' CHECK (length(source_message) <= 80),
  -- The batch it went into, while that batch waits to be sent.
  batch_id       uuid REFERENCES payment_batches(id) ON DELETE SET NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  CHECK ((saved_id IS NOT NULL) <> (account_sealed IS NOT NULL))
);
CREATE INDEX pay_collected_mine ON pay_collected (tenant_id, user_id, created_at);
CREATE UNIQUE INDEX pay_collected_once ON pay_collected (tenant_id, user_id, source_kind, source_chat, source_message) WHERE source_message <> '';
ALTER TABLE pay_collected ENABLE ROW LEVEL SECURITY;
ALTER TABLE pay_collected FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pay_collected TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON pay_collected TO taskiem_app;

-- +goose Down
DROP TABLE pay_collected;
