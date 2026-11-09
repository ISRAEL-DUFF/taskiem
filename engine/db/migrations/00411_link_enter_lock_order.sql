-- taskiem_link_enter (00291) wrote the caller's audit chain, then the other
-- business's. Two linked businesses acting at the same moment each held
-- their own chain's head and waited for the other's: a deadlock, which
-- failed one side's request with a 500. Both chains are now written in one
-- order, the lower tenant id first, so concurrent calls queue instead. Each
-- entry is the same as before; the scope ends narrowed to the other side.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_enter(p_link uuid, p_actor_type text, p_actor_id text, p_action text, p_detail jsonb)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_other uuid;
  v_mine jsonb;
  v_theirs jsonb;
BEGIN
  SELECT CASE WHEN l.a_tenant = v_me THEN l.b_tenant ELSE l.a_tenant END INTO v_other
    FROM business_links l JOIN tenants o ON o.id = CASE WHEN l.a_tenant = v_me THEN l.b_tenant ELSE l.a_tenant END AND o.status = 'active'
   WHERE l.id = p_link AND l.status = 'active' AND v_me IN (l.a_tenant, l.b_tenant);
  IF v_other IS NULL THEN
    RAISE EXCEPTION 'no such active link' USING ERRCODE = 'P0002';
  END IF;
  IF p_action !~ '^link\.[a-z_.]+$' THEN
    RAISE EXCEPTION 'a link action is named link.*' USING ERRCODE = '22023';
  END IF;
  v_mine := COALESCE(p_detail, '{}') || jsonb_build_object('business', v_other);
  v_theirs := COALESCE(p_detail, '{}') || jsonb_build_object('link', p_link, 'business', v_me);
  IF v_me < v_other THEN
    PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, p_action, p_link::text, v_mine);
    PERFORM set_config('app.tenant_scope', '{' || v_other::text || '}', true);
    PERFORM taskiem_audit_append(v_other, p_actor_type, 'link:' || v_me::text || '/' || p_actor_id, p_action, p_link::text, v_theirs);
  ELSE
    PERFORM set_config('app.tenant_scope', '{' || v_other::text || '}', true);
    PERFORM taskiem_audit_append(v_other, p_actor_type, 'link:' || v_me::text || '/' || p_actor_id, p_action, p_link::text, v_theirs);
    PERFORM set_config('app.tenant_scope', '{' || v_me::text || '}', true);
    PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, p_action, p_link::text, v_mine);
    PERFORM set_config('app.tenant_scope', '{' || v_other::text || '}', true);
  END IF;
  RETURN v_other;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_link_enter(p_link uuid, p_actor_type text, p_actor_id text, p_action text, p_detail jsonb)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_me uuid := taskiem_link_self();
  v_other uuid;
BEGIN
  SELECT CASE WHEN l.a_tenant = v_me THEN l.b_tenant ELSE l.a_tenant END INTO v_other
    FROM business_links l JOIN tenants o ON o.id = CASE WHEN l.a_tenant = v_me THEN l.b_tenant ELSE l.a_tenant END AND o.status = 'active'
   WHERE l.id = p_link AND l.status = 'active' AND v_me IN (l.a_tenant, l.b_tenant);
  IF v_other IS NULL THEN
    RAISE EXCEPTION 'no such active link' USING ERRCODE = 'P0002';
  END IF;
  IF p_action !~ '^link\.[a-z_.]+$' THEN
    RAISE EXCEPTION 'a link action is named link.*' USING ERRCODE = '22023';
  END IF;
  PERFORM taskiem_audit_append(v_me, p_actor_type, p_actor_id, p_action, p_link::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('business', v_other));
  PERFORM set_config('app.tenant_scope', '{' || v_other::text || '}', true);
  PERFORM taskiem_audit_append(v_other, p_actor_type, 'link:' || v_me::text || '/' || p_actor_id, p_action, p_link::text,
    COALESCE(p_detail, '{}') || jsonb_build_object('link', p_link, 'business', v_me));
  RETURN v_other;
END
$$;
-- +goose StatementEnd
