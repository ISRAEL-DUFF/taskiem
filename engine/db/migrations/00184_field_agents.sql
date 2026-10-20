-- +goose Up
-- Field agents (docs/spec/storefronts.md 6.10, decision 0036, 15): people
-- operators register who sign businesses up in person. An agent is a
-- Taskiem user with a platform-level agent record; they act in a business
-- only through a session: an onboarding the owner consents to with a code
-- sent to their phone (ended by publishing or after 24 hours), or a
-- support visit the owner invites. The agent is never a member; every
-- action is in the business's audit chain under the agent's name.

ALTER TABLE audit_log DROP CONSTRAINT audit_log_actor_type_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_actor_type_check CHECK (actor_type IN ('user', 'api_key', 'system', 'ai', 'end_user', 'platform_admin', 'agent'));
ALTER TABLE tenant_onboarding DROP CONSTRAINT tenant_onboarding_source_check;
ALTER TABLE tenant_onboarding ADD CONSTRAINT tenant_onboarding_source_check CHECK (source IN ('signup', 'existing', 'agent'));

-- Platform data: read and written through the functions below.
CREATE TABLE agents (
  id            uuid PRIMARY KEY,
  user_id       uuid NOT NULL UNIQUE REFERENCES users(id),
  -- The agent's own workspace, which their sign-in lands in.
  workspace_id  uuid NOT NULL REFERENCES tenants(id),
  code          text NOT NULL UNIQUE CHECK (code ~ '^AG-[A-Z2-9]{5}$'),
  name          text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
  phone         text NOT NULL,
  region        text NOT NULL DEFAULT '',
  status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'retired')),
  -- What the operator checked: {id_type, id_checked, referee_name, referee_phone}.
  checks        jsonb NOT NULL DEFAULT '{}',
  registered_by text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agent_sessions (
  id           uuid PRIMARY KEY,
  agent_id     uuid NOT NULL REFERENCES agents(id),
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  kind         text NOT NULL CHECK (kind IN ('onboarding', 'support')),
  owner_user   uuid NOT NULL REFERENCES users(id),
  status       text NOT NULL CHECK (status IN ('pending', 'active', 'ended')),
  -- An onboarding waits for the owner to send the consent code from this
  -- WhatsApp number: only its digest is kept, and wrong codes are counted.
  owner_number     text,
  consent_hash     bytea,
  consent_failures int NOT NULL DEFAULT 0,
  consent_at   timestamptz,
  expires_at   timestamptz NOT NULL,
  ended_at     timestamptz,
  end_reason   text,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_sessions_agent ON agent_sessions (agent_id, created_at DESC);
CREATE INDEX agent_sessions_tenant ON agent_sessions (tenant_id, created_at DESC);
CREATE INDEX agent_sessions_pending ON agent_sessions (owner_number) WHERE status = 'pending';

-- The agent who signed a business up.
ALTER TABLE tenants ADD COLUMN agent_id uuid REFERENCES agents(id);

ALTER TABLE agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE agents FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON agents TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON agents TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE ON agents TO taskiem_dispatch;
ALTER TABLE agent_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_sessions FORCE ROW LEVEL SECURITY;
-- A business sees the sessions on it (who came, when, why it ended).
CREATE POLICY tenant_read ON agent_sessions FOR SELECT TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT ON agent_sessions TO taskiem_app;
CREATE POLICY dispatch ON agent_sessions TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE ON agent_sessions TO taskiem_dispatch;
GRANT SELECT (id, agent_id) ON tenants TO taskiem_dispatch;
GRANT UPDATE (agent_id) ON tenants TO taskiem_dispatch;
-- Only agent_id may be written (the grant above): the consent records the agent.
CREATE POLICY dispatch_agent ON tenants FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);

-- What a business has proved, and how (spec 7.1): phone, business, or
-- agent onboarded. Each is in the audit chain too.
CREATE TABLE tenant_verifications (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  level       text NOT NULL CHECK (level IN ('phone', 'business', 'agent_onboarded')),
  method      text NOT NULL,
  actor       text NOT NULL,
  detail      jsonb NOT NULL DEFAULT '{}',
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tenant_verifications_tenant ON tenant_verifications (tenant_id, created_at DESC);
ALTER TABLE tenant_verifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_verifications FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_verifications TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON tenant_verifications TO taskiem_app;
CREATE POLICY dispatch ON tenant_verifications FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON tenant_verifications TO taskiem_dispatch;

-- +goose StatementBegin
-- The agent a user is, if any (active or not).
CREATE FUNCTION taskiem_agent_of(p_user uuid)
RETURNS TABLE (id uuid, code text, name text, status text, region text, workspace_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.id, a.code, a.name, a.status, a.region, a.workspace_id FROM agents a WHERE a.user_id = p_user
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- An active agent by their code (an owner inviting them back).
CREATE FUNCTION taskiem_agent_by_code(p_code text)
RETURNS TABLE (id uuid, name text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.id, a.name FROM agents a WHERE a.code = upper(p_code) AND a.status = 'active'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The business a request may act in through a session: the session's
-- agent is this user, active, and the session is active and unexpired.
CREATE FUNCTION taskiem_agent_session_scope(p_session uuid, p_user uuid)
RETURNS TABLE (tenant_id uuid, agent_id uuid, kind text, expires_at timestamptz, agent_name text, agent_code text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id, s.agent_id, s.kind, s.expires_at, a.name, a.code
    FROM agent_sessions s JOIN agents a ON a.id = s.agent_id
    JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
   WHERE s.id = p_session AND a.user_id = p_user AND a.status = 'active' AND s.status = 'active' AND s.expires_at > now()
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Opens a session: pending for an onboarding (until the owner sends the
-- consent code from their number), active for a support visit the owner
-- invites.
CREATE FUNCTION taskiem_agent_session_open(p_agent uuid, p_tenant uuid, p_kind text, p_owner uuid, p_expires timestamptz,
  p_number text, p_code_hash bytea)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  id uuid := gen_random_uuid();
BEGIN
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
-- A new consent code for a pending onboarding: the count of wrong codes
-- starts again, and the owner has another 30 minutes.
CREATE FUNCTION taskiem_agent_session_recode(p_session uuid, p_agent uuid, p_code_hash bytea)
RETURNS timestamptz
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE agent_sessions SET consent_hash = p_code_hash, consent_failures = 0, expires_at = now() + interval '30 minutes'
   WHERE id = p_session AND agent_id = p_agent AND status = 'pending' AND kind = 'onboarding'
  RETURNING expires_at
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The owner sent a consent code from their number. The code must match a
-- pending onboarding for that number; five wrong codes lock them all.
-- On a match the onboarding is active for 24 hours, the business records
-- its agent, and the number is bound to the owner if it is bound to no one.
-- Outcomes: ok, wrong, locked, none, number_taken.
CREATE FUNCTION taskiem_agent_session_consent(p_number text, p_code text)
RETURNS TABLE (outcome text, session_id uuid, tenant_id uuid, tenant_name text, agent_name text, agent_code text, owner_user uuid, expires_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  s agent_sessions;
  h bytea := sha256(convert_to(p_number || ':' || p_code, 'UTF8'));
  e timestamptz := now() + interval '24 hours';
  a agents;
  tname text;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM agent_sessions x WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now()) THEN
    RETURN QUERY SELECT 'none'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
  END IF;
  SELECT * INTO s FROM agent_sessions x
   WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now() AND x.consent_failures < 5 AND x.consent_hash = h
   FOR UPDATE;
  IF NOT FOUND THEN
    UPDATE agent_sessions x SET consent_failures = x.consent_failures + 1
     WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now();
    IF EXISTS (SELECT 1 FROM agent_sessions x WHERE x.owner_number = p_number AND x.status = 'pending' AND x.expires_at > now() AND x.consent_failures < 5) THEN
      RETURN QUERY SELECT 'wrong'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
    END IF;
    RETURN QUERY SELECT 'locked'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.number = p_number AND b.user_id <> s.owner_user) THEN
    RETURN QUERY SELECT 'number_taken'::text, NULL::uuid, NULL::uuid, NULL::text, NULL::text, NULL::text, NULL::uuid, NULL::timestamptz; RETURN;
  END IF;
  UPDATE agent_sessions x SET status = 'active', consent_at = now(), expires_at = e, consent_hash = NULL WHERE x.id = s.id;
  UPDATE tenants t SET agent_id = s.agent_id WHERE t.id = s.tenant_id AND t.agent_id IS NULL;
  IF NOT EXISTS (SELECT 1 FROM whatsapp_bindings b WHERE b.user_id = s.owner_user) THEN
    INSERT INTO whatsapp_bindings (user_id, number) VALUES (s.owner_user, p_number);
  END IF;
  SELECT * INTO a FROM agents x WHERE x.id = s.agent_id;
  SELECT t.name INTO tname FROM tenants t WHERE t.id = s.tenant_id;
  RETURN QUERY SELECT 'ok'::text, s.id, s.tenant_id, tname, a.name, a.code, s.owner_user, e;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The owner ends every live agent session on the businesses they own
-- (STOP AGENT on WhatsApp). Returns the businesses' names.
CREATE FUNCTION taskiem_agent_sessions_end_for_owner(p_owner uuid)
RETURNS TABLE (tenant_id uuid, agent_name text)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH x AS (
    UPDATE agent_sessions s SET status = 'ended', ended_at = now(), end_reason = 'owner_ended'
     WHERE s.status IN ('pending', 'active') AND s.expires_at > now()
       AND EXISTS (SELECT 1 FROM memberships m WHERE m.tenant_id = s.tenant_id AND m.user_id = p_owner AND m.role = 'owner')
    RETURNING s.tenant_id, s.agent_id)
  SELECT x.tenant_id, a.name FROM x JOIN agents a ON a.id = x.agent_id
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Ends a session (published, the agent or the owner ended it, expired).
CREATE FUNCTION taskiem_agent_session_end(p_session uuid, p_tenant uuid, p_reason text)
RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH x AS (UPDATE agent_sessions SET status = 'ended', ended_at = now(), end_reason = p_reason
              WHERE id = p_session AND tenant_id = p_tenant AND status IN ('pending', 'active') RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM x)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- An agent's sessions, newest first, with the business's name.
CREATE FUNCTION taskiem_agent_sessions(p_agent uuid, p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, tenant_name text, kind text, status text, consent_at timestamptz, expires_at timestamptz,
  ended_at timestamptz, end_reason text, created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.id, s.tenant_id, t.name, s.kind,
         CASE WHEN s.status <> 'ended' AND s.expires_at <= now() THEN 'ended' ELSE s.status END,
         s.consent_at, s.expires_at, s.ended_at, COALESCE(s.end_reason, CASE WHEN s.status <> 'ended' AND s.expires_at <= now() THEN 'expired' END), s.created_at
    FROM agent_sessions s JOIN tenants t ON t.id = s.tenant_id
   WHERE s.agent_id = p_agent ORDER BY s.created_at DESC LIMIT p_limit
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- One of an agent's sessions, with its owner (to send and check the
-- owner's code). The status reads 'ended' once it has expired.
CREATE FUNCTION taskiem_agent_session_get(p_session uuid, p_agent uuid)
RETURNS TABLE (tenant_id uuid, tenant_name text, owner_user uuid, owner_number text, kind text, status text, expires_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id, t.name, s.owner_user, s.owner_number, s.kind,
         CASE WHEN s.status <> 'ended' AND s.expires_at <= now() THEN 'ended' ELSE s.status END, s.expires_at
    FROM agent_sessions s JOIN tenants t ON t.id = s.tenant_id
   WHERE s.id = p_session AND s.agent_id = p_agent
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A business's agent sessions, with the agents' names and codes.
CREATE FUNCTION taskiem_tenant_agent_sessions(p_tenant uuid, p_limit int)
RETURNS TABLE (id uuid, agent_name text, agent_code text, kind text, status text, consent_at timestamptz, expires_at timestamptz,
  ended_at timestamptz, end_reason text, created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.id, a.name, a.code, s.kind,
         CASE WHEN s.status <> 'ended' AND s.expires_at <= now() THEN 'ended' ELSE s.status END,
         s.consent_at, s.expires_at, s.ended_at, COALESCE(s.end_reason, CASE WHEN s.status <> 'ended' AND s.expires_at <= now() THEN 'expired' END), s.created_at
    FROM agent_sessions s JOIN agents a ON a.id = s.agent_id
   WHERE s.tenant_id = p_tenant ORDER BY s.created_at DESC LIMIT p_limit
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Operators: register an agent (the user and workspace are made first).
CREATE FUNCTION taskiem_ops_agent_add(p_id uuid, p_user uuid, p_workspace uuid, p_code text, p_name text, p_phone text, p_region text,
  p_checks jsonb, p_by text)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO agents (id, user_id, workspace_id, code, name, phone, region, checks, registered_by)
    VALUES (p_id, p_user, p_workspace, p_code, p_name, p_phone, p_region, p_checks, p_by)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_agent_set_status(p_agent uuid, p_status text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  was text;
BEGIN
  SELECT a.status INTO was FROM agents a WHERE a.id = p_agent FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such agent' USING ERRCODE = 'P0002';
  END IF;
  IF was = 'retired' THEN
    RAISE EXCEPTION 'a retired agent stays retired: register them again' USING ERRCODE = '55000';
  END IF;
  UPDATE agents SET status = p_status, updated_at = now() WHERE id = p_agent;
  IF p_status <> 'active' THEN
    UPDATE agent_sessions SET status = 'ended', ended_at = now(), end_reason = 'agent_' || p_status
     WHERE agent_id = p_agent AND status IN ('pending', 'active');
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Operators: the agents, with what they have done.
CREATE FUNCTION taskiem_ops_agents()
RETURNS TABLE (id uuid, code text, name text, phone text, region text, status text, checks jsonb, registered_by text, created_at timestamptz,
  businesses bigint, published bigint, verified bigint, live_sessions bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.id, a.code, a.name, a.phone, a.region, a.status, a.checks, a.registered_by, a.created_at,
         (SELECT count(*) FROM tenants t WHERE t.agent_id = a.id),
         (SELECT count(*) FROM tenants t JOIN storefronts f ON f.tenant_id = t.id AND f.published_version > 0 WHERE t.agent_id = a.id),
         (SELECT count(DISTINCT v.tenant_id) FROM tenant_verifications v JOIN tenants t ON t.id = v.tenant_id
           WHERE t.agent_id = a.id AND v.level = 'agent_onboarded'),
         (SELECT count(*) FROM agent_sessions s WHERE s.agent_id = a.id AND s.status = 'active' AND s.expires_at > now())
    FROM agents a ORDER BY a.created_at DESC
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Operators: an agent's businesses.
CREATE FUNCTION taskiem_ops_agent_businesses(p_agent uuid)
RETURNS TABLE (tenant_id uuid, name text, status text, created_at timestamptz, published boolean, verified boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, t.name, t.status, t.created_at,
         EXISTS (SELECT 1 FROM storefronts f WHERE f.tenant_id = t.id AND f.published_version > 0),
         EXISTS (SELECT 1 FROM tenant_verifications v WHERE v.tenant_id = t.id AND v.level = 'agent_onboarded')
    FROM tenants t WHERE t.agent_id = p_agent ORDER BY t.created_at DESC
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_agent_of(uuid)', 'taskiem_agent_by_code(text)', 'taskiem_agent_session_scope(uuid, uuid)',
    'taskiem_agent_session_open(uuid, uuid, text, uuid, timestamptz, text, bytea)', 'taskiem_agent_session_recode(uuid, uuid, bytea)',
    'taskiem_agent_session_consent(text, text)', 'taskiem_agent_sessions_end_for_owner(uuid)',
    'taskiem_agent_session_end(uuid, uuid, text)', 'taskiem_agent_sessions(uuid, int)',
    'taskiem_agent_session_get(uuid, uuid)', 'taskiem_tenant_agent_sessions(uuid, int)',
    'taskiem_ops_agent_add(uuid, uuid, uuid, text, text, text, text, jsonb, text)', 'taskiem_ops_agent_set_status(uuid, text)',
    'taskiem_ops_agents()', 'taskiem_ops_agent_businesses(uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd
GRANT SELECT (id, tenant_id, published_version) ON storefronts TO taskiem_dispatch;

-- +goose Down
DROP FUNCTION taskiem_ops_agent_businesses(uuid);
DROP FUNCTION taskiem_ops_agents();
DROP FUNCTION taskiem_ops_agent_set_status(uuid, text);
DROP FUNCTION taskiem_ops_agent_add(uuid, uuid, uuid, text, text, text, text, jsonb, text);
DROP FUNCTION taskiem_tenant_agent_sessions(uuid, int);
DROP FUNCTION taskiem_agent_session_get(uuid, uuid);
DROP FUNCTION taskiem_agent_sessions(uuid, int);
DROP FUNCTION taskiem_agent_session_end(uuid, uuid, text);
DROP FUNCTION taskiem_agent_sessions_end_for_owner(uuid);
DROP FUNCTION taskiem_agent_session_consent(text, text);
DROP FUNCTION taskiem_agent_session_recode(uuid, uuid, bytea);
DROP FUNCTION taskiem_agent_session_open(uuid, uuid, text, uuid, timestamptz, text, bytea);
DROP FUNCTION taskiem_agent_session_scope(uuid, uuid);
DROP FUNCTION taskiem_agent_by_code(text);
DROP FUNCTION taskiem_agent_of(uuid);
DROP TABLE tenant_verifications;
DROP POLICY dispatch_agent ON tenants;
REVOKE UPDATE (agent_id) ON tenants FROM taskiem_dispatch;
ALTER TABLE tenants DROP COLUMN agent_id;
DROP TABLE agent_sessions;
DROP TABLE agents;
DELETE FROM tenant_onboarding WHERE source = 'agent';
ALTER TABLE tenant_onboarding DROP CONSTRAINT tenant_onboarding_source_check;
ALTER TABLE tenant_onboarding ADD CONSTRAINT tenant_onboarding_source_check CHECK (source IN ('signup', 'existing'));
-- The audit log is an append-only chain: entries by agents stay as written,
-- so the wider actor_type check stays too.
