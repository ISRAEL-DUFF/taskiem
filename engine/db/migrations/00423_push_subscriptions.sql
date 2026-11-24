-- Chat push (docs/spec/chat-push.md): the browsers a member allowed to be
-- sent a notification when the app is closed. One row per member, business and
-- browser. The endpoint and keys are never returned by any endpoint and are
-- deleted with the row; a push is sent only with the operator's VAPID key as
-- well, so a stolen row alone sends nothing.
-- +goose Up
CREATE TABLE push_subscriptions (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  user_id       uuid NOT NULL,
  endpoint      text NOT NULL CHECK (char_length(endpoint) <= 2048),
  endpoint_hash bytea NOT NULL,
  p256dh        text NOT NULL CHECK (char_length(p256dh) <= 200),
  auth          text NOT NULL CHECK (char_length(auth) <= 100),
  label         text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 80),
  preview       boolean NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_sent_at  timestamptz,
  last_ok_at    timestamptz,
  failed_since  timestamptz,
  UNIQUE (tenant_id, user_id, endpoint_hash)
);
CREATE INDEX push_subscriptions_user ON push_subscriptions (tenant_id, user_id, created_at);
ALTER TABLE push_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE push_subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON push_subscriptions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON push_subscriptions TO taskiem_app;

-- +goose Down
DROP TABLE push_subscriptions;
