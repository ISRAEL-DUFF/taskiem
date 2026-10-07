-- WhatsApp send retries (docs/whatsapp.md#delivery-and-retries): approval
-- requests, how runs started from WhatsApp ended, and alerts go through a
-- durable outbox, one row per person and message. A row names what to send
-- (the approval, the run, the alert) and to whom (a person, not a number):
-- the message is rendered at each attempt, so no text, decision token or
-- number is stored. Each attempt is claimed once; failures back off and,
-- after the last attempt, stay as dead letters for people to see.
-- One-time codes and chat replies are not queued: the person is waiting,
-- and asks again.

-- +goose Up
CREATE TABLE whatsapp_outbox (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  kind             text NOT NULL CHECK (kind IN ('approval', 'run_outcome', 'alert')),
  user_id          uuid NOT NULL REFERENCES users(id),
  run_id           uuid,
  step_id          text,
  level            int,
  alert_id         uuid,
  dedup_key        text NOT NULL,
  status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'dead', 'dropped')),
  attempts         int NOT NULL DEFAULT 0,
  next_attempt_at  timestamptz NOT NULL DEFAULT now(),
  last_error       text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  finished_at      timestamptz,
  UNIQUE (tenant_id, dedup_key)
);
CREATE INDEX whatsapp_outbox_due ON whatsapp_outbox (tenant_id, next_attempt_at) WHERE status = 'pending';
CREATE INDEX whatsapp_outbox_dead ON whatsapp_outbox (tenant_id, finished_at DESC) WHERE status = 'dead';
ALTER TABLE whatsapp_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON whatsapp_outbox TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON whatsapp_outbox TO taskiem_app;
-- The notifier finds tenants with something due without entering each.
CREATE POLICY dispatch ON whatsapp_outbox FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, status) ON whatsapp_outbox TO taskiem_dispatch;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT m.tenant_id FROM memberships m
    JOIN whatsapp_bindings b ON b.user_id = m.user_id
    JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
  UNION
  SELECT o.tenant_id FROM whatsapp_outbox o
    JOIN tenants t ON t.id = o.tenant_id AND t.status = 'active'
   WHERE o.status = 'pending'
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_wa_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT m.tenant_id FROM memberships m
    JOIN whatsapp_bindings b ON b.user_id = m.user_id
    JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
$$;
-- +goose StatementEnd
DROP TABLE whatsapp_outbox;
