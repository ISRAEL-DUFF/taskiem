-- WhatsApp conversations and decisions inside a tenant (spec 11.1, 11.3,
-- 9.1). A number's identity and window are the person's (00035); what it
-- is doing in a tenant (the command being confirmed, the inputs being
-- collected, the approval waiting for step-up) is the tenant's data and
-- lives here, under the tenant's row-level security, read only with the
-- conversation's current tenant in scope.

-- +goose Up
CREATE TABLE chat_sessions (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  number      text NOT NULL CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  user_id     uuid NOT NULL REFERENCES users(id),
  state       text NOT NULL DEFAULT 'idle' CHECK (state IN ('idle', 'collecting_input', 'awaiting_confirmation', 'awaiting_approval_stepup')),
  data        jsonb NOT NULL DEFAULT '{}',
  expires_at  timestamptz NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, number)
);

-- Signed decision tokens issued for approval buttons and step-up
-- hand-off links. The token carries the nonce and a MAC over every
-- column; the row makes it single use. Approve and Reject buttons sent
-- together share a notice: using one spends both.
CREATE TABLE whatsapp_tokens (
  nonce       bytea PRIMARY KEY CHECK (length(nonce) = 12),
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  purpose     text NOT NULL CHECK (purpose IN ('decide', 'handoff')),
  run_id      uuid NOT NULL,
  step_id     text NOT NULL,
  level       int NOT NULL,
  user_id     uuid NOT NULL REFERENCES users(id),
  decision    text NOT NULL CHECK (decision IN ('approved', 'rejected')),
  notice      uuid NOT NULL,
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON whatsapp_tokens (notice);
CREATE INDEX ON whatsapp_tokens (expires_at);

-- Approval requests sent to an approver by WhatsApp, once per level.
CREATE TABLE whatsapp_notices (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  run_id      uuid NOT NULL,
  step_id     text NOT NULL,
  level       int NOT NULL,
  user_id     uuid NOT NULL REFERENCES users(id),
  sent_at     timestamptz NOT NULL DEFAULT now(),
  error       text,
  PRIMARY KEY (run_id, step_id, level, user_id)
);

-- Runs started from WhatsApp: the person hears how they ended.
CREATE TABLE whatsapp_run_watches (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  run_id       uuid PRIMARY KEY,
  user_id      uuid NOT NULL REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  notified_at  timestamptz
);
CREATE INDEX ON whatsapp_run_watches (tenant_id) WHERE notified_at IS NULL;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['chat_sessions', 'whatsapp_tokens', 'whatsapp_notices', 'whatsapp_run_watches'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE whatsapp_run_watches;
DROP TABLE whatsapp_notices;
DROP TABLE whatsapp_tokens;
DROP TABLE chat_sessions;
