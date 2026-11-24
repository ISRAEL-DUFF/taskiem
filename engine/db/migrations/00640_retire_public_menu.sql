-- The separate public menu retired (build plan H6, g-ct-oldmenu; decided by
-- the founder 24 November 2026: nothing is in production, so nothing is
-- carried over). A business's customer menu is its customers' tools: the
-- workflows it deploys in prod with tool.for "customers", each with its
-- answer. Declaring and publishing such a tool is the one switch that
-- offers it, on the business's own WhatsApp number (menu) and in the app
-- alike; so the menu's own list (whatsapp_public_workflows, with the
-- command 00569 gave its entries) and its on/off switch
-- (whatsapp_numbers.public_menu) go. The down recreates both as 00569 left
-- them, empty, so 00569's own down still runs.
-- +goose Up
DROP TABLE whatsapp_public_workflows;
ALTER TABLE whatsapp_numbers DROP COLUMN public_menu;

-- +goose Down
ALTER TABLE whatsapp_numbers ADD COLUMN public_menu boolean NOT NULL DEFAULT false;
CREATE TABLE whatsapp_public_workflows (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  workflow_id  uuid NOT NULL,
  label        text NOT NULL CHECK (length(label) BETWEEN 1 AND 24),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  command      text NOT NULL,
  PRIMARY KEY (tenant_id, workflow_id)
);
ALTER TABLE whatsapp_public_workflows ADD CONSTRAINT whatsapp_public_workflows_command_check CHECK (command ~ '^[a-z][a-z0-9-]{1,23}$');
ALTER TABLE whatsapp_public_workflows ADD CONSTRAINT whatsapp_public_workflows_command_key UNIQUE (tenant_id, command);
ALTER TABLE whatsapp_public_workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_public_workflows FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON whatsapp_public_workflows TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON whatsapp_public_workflows TO taskiem_app;
