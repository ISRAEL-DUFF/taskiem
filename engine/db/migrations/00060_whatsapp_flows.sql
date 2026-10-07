-- WhatsApp Flows (spec 11.1, 11.2): forms sent inside WhatsApp, filled in
-- from Taskiem's Flows data endpoint. Each Flow sent has a random flow
-- token; only its SHA-256 is kept here, with what the Flow is for:
--   inputs  a workflow's inputs for a bound person (then confirmed in chat)
--   pin     the WhatsApp approval PIN for one decision (step-up)
--   public  a public self-service menu's inputs for an unbound number
-- Tenant data under forced row-level security: the token names its tenant,
-- and a request is handled with only that tenant in scope.

-- +goose Up
CREATE TABLE whatsapp_flows (
  token_hash       bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  kind             text NOT NULL CHECK (kind IN ('inputs', 'pin', 'public')),
  number           text NOT NULL CHECK (number ~ '^\+[1-9][0-9]{6,14}$'),
  phone_number_id  text NOT NULL,              -- the business number it was sent from
  user_id          uuid REFERENCES users(id),  -- none for public menus
  workflow_id      uuid,
  version          int,
  run_id           uuid,
  step_id          text,
  level            int,
  decision         text CHECK (decision IN ('approved', 'rejected')),
  decision_nonce   bytea,                      -- the decision token the PIN continues
  data             jsonb NOT NULL DEFAULT '{}', -- checked inputs (personal ones sealed) or the outcome
  status           text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'submitted', 'done', 'refused')),
  expires_at       timestamptz NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CHECK (kind = 'public' OR user_id IS NOT NULL),
  CHECK (kind <> 'pin' OR (run_id IS NOT NULL AND step_id IS NOT NULL AND level IS NOT NULL AND decision IS NOT NULL))
);
CREATE INDEX ON whatsapp_flows (tenant_id, number);
CREATE INDEX ON whatsapp_flows (expires_at);

ALTER TABLE whatsapp_flows ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_flows FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON whatsapp_flows TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON whatsapp_flows TO taskiem_app;

-- +goose Down
DROP TABLE whatsapp_flows;
