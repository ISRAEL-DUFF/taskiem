-- Bringing people in from chat (docs/spec/chat-way-in.md 10.2, decision
-- 0058).
--
-- An owner invites someone from chat: `invite +234… as viewer` on
-- WhatsApp or Telegram, `invite @ada as viewer` in Slack. The invitation
-- is the business's (tenant-isolated), to an address: a phone number, or
-- a Slack user. WhatsApp sends it as a template, Telegram as a link the
-- owner forwards (its token kept hashed: the link alone joins no one, the
-- person's own contact must be the number invited), Slack as a DM. A
-- member of an installed Slack workspace may also ask to join, which waits
-- for an owner's Approve. The invited person answers before they are a
-- member, so they are found by their address, through the functions below.
-- +goose Up
CREATE TABLE chat_invites (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  channel      text NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'slack')),
  address      text NOT NULL CHECK (length(address) BETWEEN 1 AND 64),
  roles        text[] NOT NULL DEFAULT '{}',
  invited_by   uuid REFERENCES users(id),
  inviter      text NOT NULL DEFAULT '' CHECK (length(inviter) <= 80), -- their name, as the invitation says it
  token_hash   bytea UNIQUE CHECK (token_hash IS NULL OR length(token_hash) = 32),
  state        text NOT NULL DEFAULT 'sent' CHECK (state IN ('asked', 'sent', 'joined', 'declined')),
  joined_user  uuid REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  answered_at  timestamptz,
  CHECK (state <> 'sent' OR cardinality(roles) > 0)
);
CREATE INDEX ON chat_invites (channel, address) WHERE state IN ('asked', 'sent');
CREATE INDEX ON chat_invites (tenant_id, created_at);
ALTER TABLE chat_invites ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_invites FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON chat_invites TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON chat_invites TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON chat_invites FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON chat_invites TO taskiem_app;
GRANT SELECT ON chat_invites TO taskiem_dispatch;

-- The invitations waiting for an address: what the invited person is
-- offered when they write or answer.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_invites_for(p_channel text, p_address text)
RETURNS TABLE (id uuid, tenant_id uuid, business text, roles text[], inviter text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT i.id, i.tenant_id, t.name, i.roles, i.inviter
    FROM chat_invites i
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE i.channel = p_channel AND i.address = p_address AND i.state = 'sent' AND i.expires_at > now()
   ORDER BY i.created_at DESC
$$;
-- +goose StatementEnd

-- One invitation, by its id (a button's) or by its Telegram link's token,
-- while it waits: its business, address and roles.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_invite(p_id uuid, p_token_hash bytea)
RETURNS TABLE (id uuid, tenant_id uuid, business text, channel text, address text, roles text[], inviter text, state text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT i.id, i.tenant_id, t.name, i.channel, i.address, i.roles, i.inviter, i.state
    FROM chat_invites i
    JOIN tenants t ON t.id = i.tenant_id AND t.status = 'active'
   WHERE (i.id = p_id OR (p_token_hash IS NOT NULL AND i.token_hash = p_token_hash)) AND i.state IN ('asked', 'sent') AND i.expires_at > now()
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_chat_invites_for(text, text)', 'taskiem_chat_invite(uuid, bytea)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_chat_invite(uuid, bytea);
DROP FUNCTION taskiem_chat_invites_for(text, text);
DROP TABLE chat_invites;
