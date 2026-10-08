-- The audit chain is written only through taskiem_audit_append (spec 5.3):
-- the application role can no longer insert rows or move a chain head
-- itself, so it cannot forge or fork a tenant's chain.

-- +goose Up
REVOKE INSERT ON audit_log FROM taskiem_app;
REVOKE USAGE ON SEQUENCE audit_log_id_seq FROM taskiem_app;
REVOKE INSERT, UPDATE ON audit_chain_heads FROM taskiem_app;

CREATE POLICY dispatch_append ON audit_log FOR INSERT TO taskiem_dispatch WITH CHECK (true);
CREATE POLICY dispatch_append ON audit_chain_heads FOR INSERT TO taskiem_dispatch WITH CHECK (true);
CREATE POLICY dispatch_advance ON audit_chain_heads FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT INSERT ON audit_log TO taskiem_dispatch;
GRANT USAGE ON SEQUENCE audit_log_id_seq TO taskiem_dispatch;
GRANT INSERT, UPDATE ON audit_chain_heads TO taskiem_dispatch;

-- Runs as its owner (taskiem_dispatch) and checks the caller's scope itself.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_audit_append(
  p_tenant uuid, p_actor_type text, p_actor_id text, p_action text, p_target text, p_detail jsonb)
RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_seq bigint;
  v_prev bytea;
  v_hash bytea;
  v_at timestamptz := date_trunc('microseconds', clock_timestamp());
BEGIN
  IF p_tenant IS NULL OR NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'tenant % not in scope', p_tenant USING ERRCODE = 'insufficient_privilege';
  END IF;

  INSERT INTO audit_chain_heads (tenant_id, chain_seq, head_hash)
  VALUES (p_tenant, 0, '\x0000000000000000000000000000000000000000000000000000000000000000')
  ON CONFLICT (tenant_id) DO NOTHING;

  SELECT chain_seq, head_hash INTO v_seq, v_prev
    FROM audit_chain_heads WHERE tenant_id = p_tenant FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'tenant % not in scope', p_tenant USING ERRCODE = 'insufficient_privilege';
  END IF;

  v_seq := v_seq + 1;
  v_hash := sha256(v_prev || taskiem_audit_canonical(
    p_tenant, v_seq, p_actor_type, p_actor_id, p_action, p_target, COALESCE(p_detail, '{}'), v_at));

  INSERT INTO audit_log (tenant_id, chain_seq, actor_type, actor_id, action, target, detail, prev_hash, hash, at)
  VALUES (p_tenant, v_seq, p_actor_type, p_actor_id, p_action, p_target, COALESCE(p_detail, '{}'), v_prev, v_hash, v_at);

  UPDATE audit_chain_heads SET chain_seq = v_seq, head_hash = v_hash WHERE tenant_id = p_tenant;
  RETURN v_seq;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_audit_append(uuid, text, text, text, text, jsonb) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_audit_append(uuid, text, text, text, text, jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_audit_append(uuid, text, text, text, text, jsonb) TO taskiem_app;

-- +goose Down
ALTER FUNCTION taskiem_audit_append(uuid, text, text, text, text, jsonb) OWNER TO CURRENT_USER;
ALTER FUNCTION taskiem_audit_append(uuid, text, text, text, text, jsonb) SECURITY INVOKER;
ALTER FUNCTION taskiem_audit_append(uuid, text, text, text, text, jsonb) RESET search_path;
GRANT EXECUTE ON FUNCTION taskiem_audit_append(uuid, text, text, text, text, jsonb) TO PUBLIC;
REVOKE INSERT, UPDATE ON audit_chain_heads FROM taskiem_dispatch;
REVOKE USAGE ON SEQUENCE audit_log_id_seq FROM taskiem_dispatch;
REVOKE INSERT ON audit_log FROM taskiem_dispatch;
DROP POLICY dispatch_advance ON audit_chain_heads;
DROP POLICY dispatch_append ON audit_chain_heads;
DROP POLICY dispatch_append ON audit_log;
GRANT SELECT, INSERT, UPDATE ON audit_chain_heads TO taskiem_app;
GRANT USAGE ON SEQUENCE audit_log_id_seq TO taskiem_app;
GRANT INSERT ON audit_log TO taskiem_app;
