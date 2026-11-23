-- Made tools M2 (decision 0073, made tools §10): a button on a tool's
-- answer or on a Tell me message. It fills the form of the tool it names,
-- as whoever presses it, with that tool's care and roles; it never runs a
-- tool by itself. Its id is minted by the server (never guessed: a row
-- per button, read only inside its business), it is held by the target
-- workflow's id, not its command, so renaming a command keeps it working,
-- and it lasts seven days. once: the first run started from it spends it.
-- tool_confirms.button carries a press through a confirm.
-- +goose Up
CREATE TABLE tool_buttons (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  environment  text NOT NULL,
  workflow_id  uuid NOT NULL REFERENCES workflows(id),
  label        text NOT NULL,
  inputs       jsonb NOT NULL DEFAULT '{}',
  once         boolean NOT NULL DEFAULT false,
  from_run     uuid,
  expires_at   timestamptz NOT NULL,
  used_by      uuid REFERENCES users(id),
  used_at      timestamptz,
  used_run     uuid,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tool_buttons_expiry ON tool_buttons (expires_at);
ALTER TABLE tool_buttons ENABLE ROW LEVEL SECURITY;
ALTER TABLE tool_buttons FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tool_buttons TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON tool_buttons TO taskiem_app;

ALTER TABLE tool_confirms ADD COLUMN button uuid;

-- +goose Down
ALTER TABLE tool_confirms DROP COLUMN button;
DROP TABLE tool_buttons;
