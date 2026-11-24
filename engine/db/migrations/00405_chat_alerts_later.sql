-- Chat tools phase 4 (docs/spec/chat-tools.md §6): an alert card a person
-- put off with Later is not shown to them again until the time it names.
-- A person's own: each sees their own alerts, put off for themselves only.
-- +goose Up
CREATE TABLE chat_alerts_later (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  user_id    uuid NOT NULL REFERENCES users(id),
  alert_key  text NOT NULL CHECK (length(alert_key) BETWEEN 1 AND 200),
  until      timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, user_id, alert_key)
);
ALTER TABLE chat_alerts_later ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_alerts_later FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON chat_alerts_later TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON chat_alerts_later TO taskiem_app;

-- +goose Down
DROP TABLE chat_alerts_later;
