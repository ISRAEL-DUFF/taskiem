-- A partner's sub-tenant cap falls back to its plan's: creating a
-- sub-tenant checked only partners.max_subtenants, whose default 0 means
-- no cap, so a Business partner (plans.yaml: max_subtenants 50) could make
-- any number. An operator's cap on the partner still wins; a partner with
-- neither (or no subscription: the internal plan) has no limit.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_partner_create_subtenant(p_id uuid, p_name text, p_region text, p_actor_type text, p_actor_id text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_partner uuid := taskiem_partner_self();
  v_max int;
BEGIN
  -- Serialise creations per partner so the cap holds.
  SELECT max_subtenants INTO v_max FROM partners WHERE tenant_id = v_partner FOR UPDATE;
  IF v_max = 0 THEN
    SELECT COALESCE((pl.partner->>'max_subtenants')::int, 0) INTO v_max
      FROM subscriptions s JOIN plans pl ON pl.id = s.plan_id
     WHERE s.tenant_id = v_partner;
  END IF;
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_partner_create_subtenant(p_id uuid, p_name text, p_region text, p_actor_type text, p_actor_id text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_partner uuid := taskiem_partner_self();
  v_max int;
BEGIN
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
