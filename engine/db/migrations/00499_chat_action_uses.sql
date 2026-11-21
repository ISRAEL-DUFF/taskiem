-- Menus in the chat, the most used first (docs/spec/chat-menus.md §6.1,
-- M2): how many times a member used each action of the chat's catalogue,
-- by day, so a topic's menu puts the five they use most in its rows. Only
-- the action's id and a count: no words, no values. Days past thirty are
-- not read, and are deleted as the next use is counted.
-- +goose Up
CREATE TABLE chat_action_uses (
  tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  action    text NOT NULL CHECK (action ~ '^[a-z]{1,20}\.[a-z]{1,20}$'),
  day       date NOT NULL,
  uses      integer NOT NULL DEFAULT 1 CHECK (uses > 0),
  PRIMARY KEY (tenant_id, user_id, action, day)
);
CREATE INDEX chat_action_uses_day ON chat_action_uses (tenant_id, user_id, day);
ALTER TABLE chat_action_uses ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_action_uses FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON chat_action_uses TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON chat_action_uses TO taskiem_app;

-- +goose Down
DROP TABLE chat_action_uses;
