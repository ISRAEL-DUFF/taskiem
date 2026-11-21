-- Paying several people (paying people, phase 3): a batch is a list of
-- payments, each prepared, checked and sent as one payment is (its own
-- run, its own key), confirmed with one passkey and, where the owner's OK
-- is needed, approved with one. Rows that fail a check are held out
-- before anything moves.
-- +goose Up
CREATE TABLE payment_batches (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  title         text NOT NULL DEFAULT '' CHECK (length(title) <= 80),
  kind          text NOT NULL DEFAULT 'list' CHECK (kind IN ('list', 'riders', 'messages')),
  status        text NOT NULL DEFAULT 'prepared' CHECK (status IN ('preparing', 'prepared', 'sent', 'cancelled')),
  copy_of       uuid REFERENCES payment_batches(id),
  created_by    text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  confirmed_by  text,
  confirmed_at  timestamptz
);
CREATE INDEX payment_batches_recent ON payment_batches (tenant_id, created_at DESC);
ALTER TABLE payment_batches ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_batches FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payment_batches TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON payment_batches TO taskiem_app;

ALTER TABLE payments ADD COLUMN batch_id uuid REFERENCES payment_batches(id);
CREATE INDEX payments_batch ON payments (batch_id) WHERE batch_id IS NOT NULL;

-- +goose Down
DROP INDEX payments_batch;
ALTER TABLE payments DROP COLUMN batch_id;
DROP TABLE payment_batches;
