-- The web's Taskiem conversation: the buttons of the answer the person has
-- not yet answered, so they can still be pressed after the page is reloaded
-- or opened in another tab.
--
-- The history keeps buttons by their titles only, never their ids, which
-- carry tokens (00295), and that stays. What this adds is bounded in the
-- same way a half-made step is (chat_sessions, 30 minutes): one row per
-- person, the ids of the newest web answer's buttons; it is replaced by the
-- next answer, deleted when the person says anything on the web (the step
-- has moved on) or when an answer carries no buttons, and expires with the
-- step. Only the person reads it (the API filters by user).
-- +goose Up
CREATE TABLE chat_live_buttons (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  user_id    uuid NOT NULL REFERENCES users(id),
  -- The history row of the answer the buttons came with.
  message_id uuid NOT NULL,
  -- {"$pii": …}: [{"id": …, "title": …}], in order, sealed (the ids carry
  -- decision tokens: never in the clear).
  buttons    jsonb NOT NULL CHECK (buttons ? '$pii'),
  expires_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, user_id)
);
CREATE INDEX chat_live_buttons_expiry ON chat_live_buttons (expires_at);
ALTER TABLE chat_live_buttons ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_live_buttons FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON chat_live_buttons TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON chat_live_buttons TO taskiem_app;

-- Retention across businesses, as the history's: the dispatch role sees only
-- when rows expire and deletes those past it, in the same purge.
CREATE POLICY dispatch_purge ON chat_live_buttons FOR DELETE TO taskiem_dispatch USING (expires_at <= now());
CREATE POLICY dispatch_read_expiry ON chat_live_buttons FOR SELECT TO taskiem_dispatch USING (expires_at <= now());
GRANT SELECT (expires_at), DELETE ON chat_live_buttons TO taskiem_dispatch;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_chat_history_purge()
RETURNS bigint LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  DELETE FROM chat_history h WHERE h.expires_at <= now();
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM chat_live_buttons b WHERE b.expires_at <= now();
  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_chat_history_purge()
RETURNS bigint LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  DELETE FROM chat_history h WHERE h.expires_at <= now();
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
-- +goose StatementEnd
DROP TABLE chat_live_buttons;
