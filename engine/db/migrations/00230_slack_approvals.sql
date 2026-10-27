-- Approvals in Slack (K2 of docs/spec/business-connectors.md; decision
-- 0051): Slack is a third chat channel for approvals, beside WhatsApp and
-- Telegram, on the same notices, outbox and decision tokens.
--
-- A member links their Slack user in a business whose workspace is
-- connected: Taskiem finds them in the workspace by their email and sends
-- a Confirm button to them in Slack; the link holds once they press it.
-- Unlike a WhatsApp number or a Telegram account, which act for a person
-- in every business they belong to, a Slack link is one business's: its
-- workspace, its bot. The address is sl:<workspace id>:<user id>.
--
-- Slack's button clicks arrive without a tenant: taskiem_slack_user_of
-- answers which members a Slack user is, by workspace, and
-- taskiem_slack_confirm completes a link by its nonce; both are definers
-- reading this table only.

-- +goose Up
CREATE TABLE slack_bindings (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  user_id       uuid NOT NULL REFERENCES users(id),
  team_id       text NOT NULL CHECK (team_id ~ '^[A-Z0-9]{1,24}$'),
  slack_user_id text NOT NULL CHECK (slack_user_id ~ '^[A-Z0-9]{1,24}$'),
  nonce_hash    bytea CHECK (nonce_hash IS NULL OR length(nonce_hash) = 32), -- the Confirm button's, until pressed
  requested_at  timestamptz NOT NULL DEFAULT now(),
  verified_at   timestamptz,
  PRIMARY KEY (tenant_id, user_id),
  CHECK ((verified_at IS NULL) = (nonce_hash IS NOT NULL))
);
CREATE UNIQUE INDEX slack_bindings_user ON slack_bindings (tenant_id, team_id, slack_user_id);
CREATE INDEX slack_bindings_lookup ON slack_bindings (team_id, slack_user_id);
ALTER TABLE slack_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE slack_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON slack_bindings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON slack_bindings TO taskiem_app;
CREATE POLICY dispatch ON slack_bindings TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON slack_bindings TO taskiem_dispatch;

-- The members a Slack user is, in the businesses that linked them.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_user_of(p_team text, p_user text)
RETURNS TABLE (tenant_id uuid, user_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.tenant_id, b.user_id FROM slack_bindings b
    JOIN tenants t ON t.id = b.tenant_id AND t.status = 'active'
   WHERE b.team_id = p_team AND b.slack_user_id = p_user AND b.verified_at IS NOT NULL
$$;
-- +goose StatementEnd

-- Completes a link: the pending one with this nonce, pressed by the Slack
-- user it was sent to. Returns its tenant, or nothing.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_confirm(p_nonce_hash bytea, p_team text, p_user text)
RETURNS uuid
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE slack_bindings SET verified_at = now(), nonce_hash = NULL
   WHERE nonce_hash = p_nonce_hash AND team_id = p_team AND slack_user_id = p_user
     AND verified_at IS NULL AND requested_at > now() - interval '1 day'
  RETURNING tenant_id
$$;
-- +goose StatementEnd

-- Tenants with at least one member who has linked Slack.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT b.tenant_id FROM slack_bindings b
    JOIN tenants t ON t.id = b.tenant_id AND t.status = 'active'
   WHERE b.verified_at IS NOT NULL
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_slack_user_of(text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_slack_confirm(bytea, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_slack_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_slack_user_of(text, text), taskiem_slack_confirm(bytea, text, text), taskiem_slack_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_slack_user_of(text, text), taskiem_slack_confirm(bytea, text, text), taskiem_slack_tenants() TO taskiem_app;

-- The shared conversation tables take sl: addresses, and the approval
-- tables the slack channel.
ALTER TABLE chat_sessions DROP CONSTRAINT chat_sessions_number_check;
ALTER TABLE chat_sessions ADD CONSTRAINT chat_sessions_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$' OR number ~ '^sl:[A-Z0-9]{1,24}:[A-Z0-9]{1,24}$');
ALTER TABLE channel_languages DROP CONSTRAINT channel_languages_number_check;
ALTER TABLE channel_languages ADD CONSTRAINT channel_languages_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$' OR number ~ '^sl:[A-Z0-9]{1,24}:[A-Z0-9]{1,24}$');
ALTER TABLE whatsapp_notices DROP CONSTRAINT whatsapp_notices_channel_check;
ALTER TABLE whatsapp_notices ADD CONSTRAINT whatsapp_notices_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_channel_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));
ALTER TABLE whatsapp_run_watches DROP CONSTRAINT whatsapp_run_watches_channel_check;
ALTER TABLE whatsapp_run_watches ADD CONSTRAINT whatsapp_run_watches_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));
ALTER TABLE whatsapp_tokens DROP CONSTRAINT whatsapp_tokens_channel_check;
ALTER TABLE whatsapp_tokens ADD CONSTRAINT whatsapp_tokens_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));

-- +goose Down
DELETE FROM whatsapp_tokens WHERE channel = 'slack';
ALTER TABLE whatsapp_tokens DROP CONSTRAINT whatsapp_tokens_channel_check;
ALTER TABLE whatsapp_tokens ADD CONSTRAINT whatsapp_tokens_channel_check CHECK (channel IN ('whatsapp', 'telegram'));
DELETE FROM whatsapp_run_watches WHERE channel = 'slack';
ALTER TABLE whatsapp_run_watches DROP CONSTRAINT whatsapp_run_watches_channel_check;
ALTER TABLE whatsapp_run_watches ADD CONSTRAINT whatsapp_run_watches_channel_check CHECK (channel IN ('whatsapp', 'telegram'));
DELETE FROM whatsapp_outbox WHERE channel = 'slack';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_channel_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_channel_check CHECK (channel IN ('whatsapp', 'telegram'));
DELETE FROM whatsapp_notices WHERE channel = 'slack';
ALTER TABLE whatsapp_notices DROP CONSTRAINT whatsapp_notices_channel_check;
ALTER TABLE whatsapp_notices ADD CONSTRAINT whatsapp_notices_channel_check CHECK (channel IN ('whatsapp', 'telegram'));
DELETE FROM channel_languages WHERE number LIKE 'sl:%';
ALTER TABLE channel_languages DROP CONSTRAINT channel_languages_number_check;
ALTER TABLE channel_languages ADD CONSTRAINT channel_languages_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$');
DELETE FROM chat_sessions WHERE number LIKE 'sl:%';
ALTER TABLE chat_sessions DROP CONSTRAINT chat_sessions_number_check;
ALTER TABLE chat_sessions ADD CONSTRAINT chat_sessions_number_check CHECK (number ~ '^\+[1-9][0-9]{6,14}$' OR number ~ '^tg:[1-9][0-9]{0,19}$');
DROP FUNCTION taskiem_slack_tenants();
DROP FUNCTION taskiem_slack_confirm(bytea, text, text);
DROP FUNCTION taskiem_slack_user_of(text, text);
DROP TABLE slack_bindings;
