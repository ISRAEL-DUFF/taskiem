-- Partners and sub-tenants (spec 13.1, 5.3; decision 0015).
--
-- A sub-tenant is a tenants row with parent_id set: a partner's end
-- customer, with its own audit chain and KEK (both keyed by tenant id).
-- Its data is isolated from other sub-tenants and from the partner's own
-- users: no membership puts a sub-tenant in scope. The only way in is
-- taskiem_partner_enter, which checks the parent, writes the access to both
-- audit chains, and narrows the transaction's scope to that one sub-tenant.

-- +goose Up

-- An operator makes a tenant a partner (taskiem tenants partner), with its
-- partner-wide caps (0: no cap). Nothing in the application writes it.
CREATE TABLE partners (
  tenant_id                uuid PRIMARY KEY REFERENCES tenants(id),
  max_subtenants           int    NOT NULL DEFAULT 0 CHECK (max_subtenants >= 0),
  subtenant_runs_per_day   bigint NOT NULL DEFAULT 0 CHECK (subtenant_runs_per_day >= 0),   -- across all its sub-tenants
  subtenant_runs_per_month bigint NOT NULL DEFAULT 0 CHECK (subtenant_runs_per_month >= 0),
  updated_by               text NOT NULL,
  updated_at               timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE partners ENABLE ROW LEVEL SECURITY;
ALTER TABLE partners FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON partners FOR SELECT TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON partners TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT ON partners TO taskiem_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON partners TO taskiem_dispatch;

-- A tenant's parent is fixed when it is created.
-- +goose StatementBegin
CREATE FUNCTION taskiem_tenant_parent_fixed() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.parent_id IS DISTINCT FROM OLD.parent_id THEN
    RAISE EXCEPTION 'a tenant''s parent cannot change' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tenants_parent_fixed BEFORE UPDATE OF parent_id ON tenants
  FOR EACH ROW EXECUTE FUNCTION taskiem_tenant_parent_fixed();

-- The definer functions below create sub-tenants and read what they need.
GRANT SELECT (name, region, plan_id, created_at) ON tenants TO taskiem_dispatch;
GRANT INSERT (id, parent_id, name, region, plan_id) ON tenants TO taskiem_dispatch;
CREATE POLICY dispatch_subtenant ON tenants FOR INSERT TO taskiem_dispatch WITH CHECK (parent_id IS NOT NULL);
CREATE POLICY dispatch ON tenant_usage FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON tenant_usage TO taskiem_dispatch;

-- Memberships never reach sub-tenants: a partner's users get the partner's
-- scope only. Sub-tenant scope comes from taskiem_partner_enter alone.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_tenant_scope(p_user uuid, p_include_subtenants boolean DEFAULT false)
RETURNS uuid[]
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_include_subtenants THEN
    RAISE EXCEPTION 'sub-tenants are reached only through the partner API (taskiem_partner_enter)' USING ERRCODE = '42501';
  END IF;
  RETURN (SELECT COALESCE(array_agg(DISTINCT m.tenant_id ORDER BY m.tenant_id), '{}') FROM memberships m
            JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
           WHERE m.user_id = p_user);
END
$$;
-- +goose StatementEnd

-- The partner a transaction acts for: its scope must be exactly one active
-- partner that is not itself a sub-tenant.
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_self() RETURNS uuid
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_scope uuid[] := taskiem_tenant_scope();
BEGIN
  IF cardinality(v_scope) <> 1 OR NOT EXISTS (
      SELECT 1 FROM partners p JOIN tenants t ON t.id = p.tenant_id
       WHERE p.tenant_id = v_scope[1] AND t.status = 'active' AND t.parent_id IS NULL) THEN
    RAISE EXCEPTION 'not a partner' USING ERRCODE = '42501';
  END IF;
  RETURN v_scope[1];
END
$$;
-- +goose StatementEnd

-- Enter one sub-tenant: the transaction's scope must be its partner. The
-- access is written to the partner's chain, the scope narrowed to the
-- sub-tenant alone (for the rest of the transaction), and the access written
-- to the sub-tenant's chain too. Suspended sub-tenants can be entered (to
-- read or resume them); deleted ones cannot.
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_enter(p_sub uuid, p_actor_type text, p_actor_id text, p_action text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_partner uuid := taskiem_partner_self();
BEGIN
  IF NOT EXISTS (SELECT 1 FROM tenants WHERE id = p_sub AND parent_id = v_partner AND status <> 'deleted') THEN
    RAISE EXCEPTION 'no such sub-tenant' USING ERRCODE = 'P0002';
  END IF;
  PERFORM taskiem_audit_append(v_partner, p_actor_type, p_actor_id, p_action, p_sub::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('sub_tenant', p_sub));
  PERFORM set_config('app.tenant_scope', '{' || p_sub::text || '}', true);
  PERFORM taskiem_audit_append(p_sub, p_actor_type, 'partner:' || v_partner::text || '/' || p_actor_id, p_action, p_sub::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('partner', v_partner));
END
$$;
-- +goose StatementEnd

-- Create a sub-tenant of the partner in scope, within its max_subtenants,
-- and enter it (as taskiem_partner_enter). It copies the partner's plan and,
-- unless given, its region.
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_create_subtenant(p_id uuid, p_name text, p_region text, p_actor_type text, p_actor_id text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_partner uuid := taskiem_partner_self();
  v_max int;
BEGIN
  -- Serialise creations per partner so the cap holds.
  SELECT max_subtenants INTO v_max FROM partners WHERE tenant_id = v_partner FOR UPDATE;
  IF v_max > 0 AND (SELECT count(*) FROM tenants WHERE parent_id = v_partner AND status <> 'deleted') >= v_max THEN
    RAISE EXCEPTION 'this partner may have at most % sub-tenants', v_max USING ERRCODE = '53400';
  END IF;
  INSERT INTO tenants (id, parent_id, name, region, plan_id)
    SELECT p_id, v_partner, p_name, COALESCE(NULLIF(p_region, ''), t.region), t.plan_id FROM tenants t WHERE t.id = v_partner;
  PERFORM taskiem_audit_append(v_partner, p_actor_type, p_actor_id, 'partner.subtenant.create', p_id::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('sub_tenant', p_id));
  PERFORM set_config('app.tenant_scope', '{' || p_id::text || '}', true);
  PERFORM taskiem_audit_append(p_id, p_actor_type, 'partner:' || v_partner::text || '/' || p_actor_id, 'tenant.create', p_id::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('partner', v_partner));
END
$$;
-- +goose StatementEnd

-- The partner's sub-tenants (names and status only: no sub-tenant data).
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_subtenants()
RETURNS TABLE (id uuid, name text, region text, status text, created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, t.name, t.region, t.status, t.created_at FROM tenants t
   WHERE t.parent_id = taskiem_partner_self() AND t.status <> 'deleted'
   ORDER BY t.created_at, t.id
$$;
-- +goose StatementEnd

-- A sub-tenant's inheritance (spec 13.1): its partner's own limits (the
-- non-null tenant_limits columns) and partner-wide run caps. No row for a
-- tenant without a parent. The tenant must be in scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_parent_limits(p_tenant uuid)
RETURNS TABLE (parent_id uuid, overrides jsonb, runs_per_day bigint, runs_per_month bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.parent_id,
         COALESCE((SELECT jsonb_strip_nulls(to_jsonb(l) - 'tenant_id' - 'updated_by' - 'updated_at') FROM tenant_limits l WHERE l.tenant_id = t.parent_id), '{}'),
         COALESCE(p.subtenant_runs_per_day, 0), COALESCE(p.subtenant_runs_per_month, 0)
    FROM tenants t LEFT JOIN partners p ON p.tenant_id = t.parent_id
   WHERE t.id = p_tenant AND t.parent_id IS NOT NULL AND p_tenant = ANY (taskiem_tenant_scope())
$$;
-- +goose StatementEnd

-- Runs started today and this month (UTC) by each of a partner's
-- sub-tenants: counts only. Callable by the partner, or by one of its
-- sub-tenants (to check the partner-wide caps when starting a run).
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_usage(p_partner uuid)
RETURNS TABLE (tenant_id uuid, runs_today bigint, runs_month bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id,
         COALESCE(sum(u.runs_started) FILTER (WHERE u.day = (now() AT TIME ZONE 'UTC')::date), 0)::bigint,
         COALESCE(sum(u.runs_started), 0)::bigint
    FROM tenants t
    LEFT JOIN tenant_usage u ON u.tenant_id = t.id AND u.day >= date_trunc('month', now() AT TIME ZONE 'UTC')::date
   WHERE t.parent_id = p_partner
     AND (p_partner = ANY (taskiem_tenant_scope())
          OR EXISTS (SELECT 1 FROM tenants s WHERE s.id = ANY (taskiem_tenant_scope()) AND s.parent_id = p_partner))
   GROUP BY t.id
$$;
-- +goose StatementEnd

-- Operators make a tenant a partner, change its caps, or (p_enabled false)
-- stop it being one: its sub-tenants stay, unreachable until re-enabled.
-- +goose StatementBegin
CREATE FUNCTION taskiem_set_partner(p_tenant uuid, p_enabled boolean, p_max_subtenants int, p_runs_per_day bigint, p_runs_per_month bigint, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % is not in scope', p_tenant USING ERRCODE = '42501';
  END IF;
  IF NOT p_enabled THEN
    DELETE FROM partners WHERE tenant_id = p_tenant;
    RETURN;
  END IF;
  IF EXISTS (SELECT 1 FROM tenants WHERE id = p_tenant AND parent_id IS NOT NULL) THEN
    RAISE EXCEPTION 'a sub-tenant cannot be a partner' USING ERRCODE = '22023';
  END IF;
  INSERT INTO partners (tenant_id, max_subtenants, subtenant_runs_per_day, subtenant_runs_per_month, updated_by)
  VALUES (p_tenant, COALESCE(p_max_subtenants, 0), COALESCE(p_runs_per_day, 0), COALESCE(p_runs_per_month, 0), p_by)
  ON CONFLICT (tenant_id) DO UPDATE SET
    max_subtenants = COALESCE(p_max_subtenants, partners.max_subtenants),
    subtenant_runs_per_day = COALESCE(p_runs_per_day, partners.subtenant_runs_per_day),
    subtenant_runs_per_month = COALESCE(p_runs_per_month, partners.subtenant_runs_per_month),
    updated_by = p_by, updated_at = now();
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_partner_self() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_partner_enter(uuid, text, text, text, jsonb) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_partner_create_subtenant(uuid, text, text, text, text, jsonb) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_partner_subtenants() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_parent_limits(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_partner_usage(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_set_partner(uuid, boolean, int, bigint, bigint, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_partner_self(), taskiem_partner_enter(uuid, text, text, text, jsonb),
  taskiem_partner_create_subtenant(uuid, text, text, text, text, jsonb), taskiem_partner_subtenants(), taskiem_parent_limits(uuid),
  taskiem_partner_usage(uuid), taskiem_set_partner(uuid, boolean, int, bigint, bigint, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_partner_self(), taskiem_partner_enter(uuid, text, text, text, jsonb),
  taskiem_partner_create_subtenant(uuid, text, text, text, text, jsonb), taskiem_partner_subtenants(), taskiem_parent_limits(uuid),
  taskiem_partner_usage(uuid), taskiem_set_partner(uuid, boolean, int, bigint, bigint, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_set_partner(uuid, boolean, int, bigint, bigint, text);
DROP FUNCTION taskiem_partner_usage(uuid);
DROP FUNCTION taskiem_parent_limits(uuid);
DROP FUNCTION taskiem_partner_subtenants();
DROP FUNCTION taskiem_partner_create_subtenant(uuid, text, text, text, text, jsonb);
DROP FUNCTION taskiem_partner_enter(uuid, text, text, text, jsonb);
DROP FUNCTION taskiem_partner_self();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_tenant_scope(p_user uuid, p_include_subtenants boolean DEFAULT false)
RETURNS uuid[]
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH own AS (
    SELECT DISTINCT m.tenant_id FROM memberships m
      JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
     WHERE m.user_id = p_user),
  subs AS (
    SELECT t.id FROM tenants t
     WHERE p_include_subtenants AND t.status = 'active' AND t.parent_id IN (SELECT tenant_id FROM own))
  SELECT COALESCE(array_agg(id ORDER BY id), '{}') FROM (
    SELECT tenant_id AS id FROM own UNION SELECT id FROM subs) s
$$;
-- +goose StatementEnd
REVOKE SELECT ON tenant_usage FROM taskiem_dispatch;
DROP POLICY dispatch ON tenant_usage;
DROP POLICY dispatch_subtenant ON tenants;
REVOKE INSERT (id, parent_id, name, region, plan_id) ON tenants FROM taskiem_dispatch;
REVOKE SELECT (name, region, plan_id, created_at) ON tenants FROM taskiem_dispatch;
DROP TRIGGER tenants_parent_fixed ON tenants;
DROP FUNCTION taskiem_tenant_parent_fixed();
DROP TABLE partners;
