-- Signing up from Slack (docs/spec/chat-way-in.md 9, decision 0058).
--
-- Add to Slack from Taskiem's website installs Taskiem's app in a
-- workspace with no business behind it yet. The install request (its
-- state, bound to the browser that started it, and the PKCE verifier) is
-- the platform's, not a tenant's: kept ten minutes and taken once. The
-- install it makes (the workspace, the installer, the bot token) waits a
-- day for the installer to make a business from Slack; the token is
-- sealed with the platform's KMS key, as a customer key's credentials are,
-- and given back to Slack when no business took it. When the workspace is
-- already some business's (decision 0051), the install's token is also
-- theirs (shared) and is never given back. Both tables are reached only
-- through the functions below.
-- +goose Up
CREATE TABLE slack_install_requests (
  state_hash  bytea PRIMARY KEY CHECK (length(state_hash) = 32),
  verifier    text NOT NULL,  -- the PKCE verifier, sealed with the platform's key
  kms_key     text NOT NULL,
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz
);
CREATE INDEX ON slack_install_requests (expires_at);

CREATE TABLE slack_installs (
  team_id     text PRIMARY KEY CHECK (team_id ~ '^[A-Z0-9]{1,24}$'),
  team_name   text NOT NULL CHECK (length(team_name) <= 200),
  installer   text NOT NULL CHECK (installer ~ '^[A-Z0-9]{1,24}$'),
  app_id      text CHECK (app_id ~ '^[A-Z0-9]{1,24}$'),
  token       text NOT NULL,  -- the bot token, sealed with the platform's key
  kms_key     text NOT NULL,
  -- The installer's name and email as Slack gives them: the email is
  -- never taken as confirmed.
  person      text NOT NULL DEFAULT '' CHECK (length(person) <= 200),
  email       text CHECK (length(email) <= 320),
  shared      boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL
);
CREATE INDEX ON slack_installs (expires_at);

-- A sign-up's answers wait in chat_signups on Slack too.
ALTER TABLE chat_signups DROP CONSTRAINT chat_signups_channel_check;
ALTER TABLE chat_signups ADD CONSTRAINT chat_signups_channel_check CHECK (channel IN ('whatsapp', 'telegram', 'slack'));

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['slack_install_requests', 'slack_installs'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY no_direct_access ON %I TO taskiem_app USING (false) WITH CHECK (false)', t);
    EXECUTE format('CREATE POLICY dispatch ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', t);
    EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_dispatch', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- Records an install request; requests an hour past their expiry go.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_install_request_put(p_state_hash bytea, p_verifier text, p_kms_key text, p_ttl interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM slack_install_requests r WHERE r.expires_at < now() - interval '1 hour';
  INSERT INTO slack_install_requests (state_hash, verifier, kms_key, expires_at) VALUES (p_state_hash, p_verifier, p_kms_key, now() + p_ttl);
$$;
-- +goose StatementEnd

-- Takes an install request, once and in time: its sealed verifier.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_install_request_take(p_state_hash bytea)
RETURNS TABLE (verifier text, kms_key text)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE slack_install_requests r SET used_at = now()
   WHERE r.state_hash = p_state_hash AND r.used_at IS NULL AND r.expires_at > now()
  RETURNING r.verifier, r.kms_key
$$;
-- +goose StatementEnd

-- Records a workspace's install, replacing an earlier one not yet used.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_install_put(p_team text, p_team_name text, p_installer text, p_app_id text, p_token text, p_kms_key text,
  p_person text, p_email text, p_shared boolean, p_ttl interval)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO slack_installs AS i (team_id, team_name, installer, app_id, token, kms_key, person, email, shared, expires_at)
  VALUES (p_team, p_team_name, p_installer, NULLIF(p_app_id, ''), p_token, p_kms_key, p_person, NULLIF(p_email, ''), p_shared, now() + p_ttl)
  ON CONFLICT (team_id) DO UPDATE SET team_name = EXCLUDED.team_name, installer = EXCLUDED.installer, app_id = EXCLUDED.app_id,
    token = EXCLUDED.token, kms_key = EXCLUDED.kms_key, person = EXCLUDED.person, email = EXCLUDED.email, shared = EXCLUDED.shared,
    created_at = now(), expires_at = EXCLUDED.expires_at
$$;
-- +goose StatementEnd

-- A workspace's install waiting for a business.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_install_get(p_team text)
RETURNS TABLE (team_name text, installer text, app_id text, token text, kms_key text, person text, email text, shared boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT i.team_name, i.installer, i.app_id, i.token, i.kms_key, i.person, i.email, i.shared
    FROM slack_installs i WHERE i.team_id = p_team AND i.expires_at > now()
$$;
-- +goose StatementEnd

-- Forgets a workspace's install: a business took it, or Slack says the app
-- is gone.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_install_drop(p_team text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM slack_installs i WHERE i.team_id = p_team
$$;
-- +goose StatementEnd

-- Removes the installs no business took in time, returning each so a
-- token only this install held is given back to Slack.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_installs_expired()
RETURNS TABLE (team_id text, token text, kms_key text, shared boolean)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM slack_installs i WHERE i.expires_at <= now() RETURNING i.team_id, i.token, i.kms_key, i.shared
$$;
-- +goose StatementEnd

-- The businesses whose Slack connections run on Taskiem's app in a
-- workspace (decision 0051), with each such connection, whatever its
-- status (an uninstall expired them): a new install's token is written
-- into every one. The app account row names the connection; dispatch
-- reads only active connections, so they are not joined.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_team_businesses(p_team text)
RETURNS TABLE (tenant_id uuid, name text, connection_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.tenant_id, t.name, a.connection_id
    FROM connection_app_accounts a
    JOIN tenants t ON t.id = a.tenant_id AND t.status <> 'deleted'
   WHERE a.connector = 'slack' AND a.account_key = p_team AND p_team <> ''
   ORDER BY t.created_at, a.connection_id
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_slack_install_request_put(bytea, text, text, interval)', 'taskiem_slack_install_request_take(bytea)',
    'taskiem_slack_install_put(text, text, text, text, text, text, text, text, boolean, interval)', 'taskiem_slack_install_get(text)',
    'taskiem_slack_install_drop(text)', 'taskiem_slack_installs_expired()', 'taskiem_slack_team_businesses(text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_slack_team_businesses(text);
DROP FUNCTION taskiem_slack_installs_expired();
DROP FUNCTION taskiem_slack_install_drop(text);
DROP FUNCTION taskiem_slack_install_get(text);
DROP FUNCTION taskiem_slack_install_put(text, text, text, text, text, text, text, text, boolean, interval);
DROP FUNCTION taskiem_slack_install_request_take(bytea);
DROP FUNCTION taskiem_slack_install_request_put(bytea, text, text, interval);
DELETE FROM chat_signups WHERE channel = 'slack';
ALTER TABLE chat_signups DROP CONSTRAINT chat_signups_channel_check;
ALTER TABLE chat_signups ADD CONSTRAINT chat_signups_channel_check CHECK (channel IN ('whatsapp', 'telegram'));
DROP TABLE slack_installs;
DROP TABLE slack_install_requests;
