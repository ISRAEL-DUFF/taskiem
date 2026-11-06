-- A Taskiem operator's suspension holds against the partner (F40).
-- tenants.status had one 'suspended' value whatever suspended it, and
-- taskiem_partner_enter admitted any sub-tenant not deleted, for every
-- action: a partner's key could resume a sub-tenant an operator had
-- suspended for fraud or abuse, and invite people, install workflows and
-- add connections into it meanwhile. A suspension now records who made it
-- (suspended_by). While an operator holds it:
--   * taskiem_partner_enter admits only the partner's reads (actions ending
--     in .read, and partner.connection.list) and the revocation of end-user
--     tokens; any other action, resume included, is refused (42501);
--   * a trigger refuses to lift the suspension, or to change who holds it,
--     unless the transaction is the operator console's (the platform chain,
--     ffffffff-..., in its scope: opsTx), so no other path of the
--     application role can either.
-- An operator's suspension may take over a partner's; resuming clears
-- suspended_by. Existing suspensions take their source from the last
-- suspension in the tenant's own audit chain (the partner's route writes
-- partner.subtenant.suspend, the console tenant.suspend); one with neither
-- is held as an operator's, the side that cannot be undone by the partner.
-- +goose Up
ALTER TABLE tenants ADD COLUMN suspended_by text CHECK (suspended_by IN ('partner', 'operator'));
ALTER TABLE tenants ADD CONSTRAINT tenants_suspended_by_suspended CHECK (suspended_by IS NULL OR status = 'suspended');
GRANT SELECT (suspended_by) ON tenants TO taskiem_dispatch;

UPDATE tenants t SET suspended_by = CASE (
    SELECT a.action FROM audit_log a
     WHERE a.tenant_id = t.id AND a.action IN ('partner.subtenant.suspend', 'tenant.suspend')
     ORDER BY a.chain_seq DESC LIMIT 1)
  WHEN 'partner.subtenant.suspend' THEN 'partner' ELSE 'operator' END
 WHERE t.status = 'suspended';

-- +goose StatementBegin
CREATE FUNCTION taskiem_tenant_suspension_source() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  v_ops boolean := 'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid = ANY (taskiem_tenant_scope());
BEGIN
  IF NEW.status IS DISTINCT FROM OLD.status AND NEW.status <> 'suspended' THEN
    NEW.suspended_by := NULL;
  END IF;
  IF OLD.suspended_by = 'operator' AND NEW.suspended_by IS DISTINCT FROM 'operator' AND NOT v_ops THEN
    RAISE EXCEPTION 'suspended by Taskiem: only a Taskiem operator can resume it' USING ERRCODE = '42501';
  END IF;
  IF NEW.suspended_by = 'operator' AND OLD.suspended_by IS DISTINCT FROM 'operator' AND NOT v_ops THEN
    RAISE EXCEPTION 'only a Taskiem operator suspends on Taskiem''s behalf' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tenants_suspension_source BEFORE UPDATE OF status, suspended_by ON tenants
  FOR EACH ROW EXECUTE FUNCTION taskiem_tenant_suspension_source();

-- Enter one sub-tenant (00045), refusing the partner's writes while an
-- operator holds it suspended.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_partner_enter(p_sub uuid, p_actor_type text, p_actor_id text, p_action text, p_detail jsonb)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_partner uuid := taskiem_partner_self();
  v_held boolean;
BEGIN
  SELECT t.suspended_by IS NOT DISTINCT FROM 'operator' INTO v_held
    FROM tenants t WHERE t.id = p_sub AND t.parent_id = v_partner AND t.status <> 'deleted';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no such sub-tenant' USING ERRCODE = 'P0002';
  END IF;
  IF v_held AND NOT (p_action LIKE 'partner.%.read' OR p_action IN ('partner.connection.list', 'partner.end_user_token.revoke')) THEN
    RAISE EXCEPTION 'the sub-tenant is suspended by Taskiem: its partner can read it, but only a Taskiem operator can resume it' USING ERRCODE = '42501';
  END IF;
  PERFORM taskiem_audit_append(v_partner, p_actor_type, p_actor_id, p_action, p_sub::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('sub_tenant', p_sub));
  PERFORM set_config('app.tenant_scope', '{' || p_sub::text || '}', true);
  PERFORM taskiem_audit_append(p_sub, p_actor_type, 'partner:' || v_partner::text || '/' || p_actor_id, p_action, p_sub::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('partner', v_partner));
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_partner_enter(uuid, text, text, text, jsonb) OWNER TO taskiem_dispatch;
