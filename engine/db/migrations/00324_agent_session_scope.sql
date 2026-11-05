-- The agent-session definers act only on a tenant in scope (F83).
-- taskiem_agent_session_open, taskiem_agent_session_end and
-- taskiem_tenant_agent_sessions run as taskiem_dispatch and took any
-- p_tenant: SQL of the application role in one business's transaction
-- could open an active support session in another (which the agent then
-- uses to act there, with nothing in that business's audit chain), list
-- its visits or end them. The tenant boundary rested on every Go caller
-- passing its own tenant. They now check p_tenant against
-- taskiem_tenant_scope(), as taskiem_set_tenant_limits does: opening is
-- refused (42501), listing and ending find nothing. Every caller already
-- runs in that tenant's scope except GET /v1/agent-visits, which now does
-- too. Bodies otherwise as in 00184.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_agent_session_open(p_agent uuid, p_tenant uuid, p_kind text, p_owner uuid, p_expires timestamptz,
  p_number text, p_code_hash bytea)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  id uuid := gen_random_uuid();
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM agents a WHERE a.id = p_agent AND a.status = 'active') THEN
    RAISE EXCEPTION 'no such active agent' USING ERRCODE = '42501';
  END IF;
  -- One live session per agent and business.
  UPDATE agent_sessions SET status = 'ended', ended_at = now(), end_reason = 'replaced'
   WHERE agent_id = p_agent AND tenant_id = p_tenant AND status IN ('pending', 'active');
  INSERT INTO agent_sessions (id, agent_id, tenant_id, kind, owner_user, status, consent_at, expires_at, owner_number, consent_hash)
    VALUES (id, p_agent, p_tenant, p_kind, p_owner, CASE WHEN p_kind = 'support' THEN 'active' ELSE 'pending' END,
      CASE WHEN p_kind = 'support' THEN now() END, p_expires, p_number, p_code_hash);
  RETURN id;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_agent_session_end(p_session uuid, p_tenant uuid, p_reason text)
RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH x AS (UPDATE agent_sessions SET status = 'ended', ended_at = now(), end_reason = p_reason
              WHERE id = p_session AND tenant_id = p_tenant AND p_tenant = ANY (taskiem_tenant_scope())
                AND status IN ('pending', 'active') RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM x)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_tenant_agent_sessions(p_tenant uuid, p_limit int)
RETURNS TABLE (id uuid, agent_name text, agent_code text, kind text, status text, consent_at timestamptz, expires_at timestamptz,
  ended_at timestamptz, end_reason text, created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.id, a.name, a.code, s.kind,
         CASE WHEN s.status <> 'ended' AND s.expires_at <= now() THEN 'ended' ELSE s.status END,
         s.consent_at, s.expires_at, s.ended_at, COALESCE(s.end_reason, CASE WHEN s.status <> 'ended' AND s.expires_at <= now() THEN 'expired' END), s.created_at
    FROM agent_sessions s JOIN agents a ON a.id = s.agent_id
   WHERE s.tenant_id = p_tenant AND p_tenant = ANY (taskiem_tenant_scope())
   ORDER BY s.created_at DESC LIMIT p_limit
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_agent_session_open(uuid, uuid, text, uuid, timestamptz, text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_agent_session_end(uuid, uuid, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tenant_agent_sessions(uuid, int) OWNER TO taskiem_dispatch;
