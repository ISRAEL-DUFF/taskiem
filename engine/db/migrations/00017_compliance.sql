-- Audit-chain anchoring (spec 9.2) and tenant retention defaults (9.4).

-- +goose Up
-- A signed record of a tenant's chain head at a moment, also delivered
-- outside the database. An export that disagrees with an anchor was
-- rewritten after the anchor was made.
CREATE TABLE audit_anchors (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  chain_seq   bigint NOT NULL,
  head_hash   bytea NOT NULL CHECK (length(head_hash) = 32),
  anchored_at timestamptz NOT NULL,
  key_id      text NOT NULL,
  signature   bytea NOT NULL,
  PRIMARY KEY (tenant_id, chain_seq)
);

ALTER TABLE audit_anchors ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_anchors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_anchors TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON audit_anchors TO taskiem_app;

-- Anchors are append-only, like the chain they anchor.
-- +goose StatementBegin
CREATE FUNCTION taskiem_audit_anchors_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'audit anchors are append-only' USING ERRCODE = 'check_violation';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER audit_anchors_append_only BEFORE UPDATE OR DELETE ON audit_anchors
  FOR EACH ROW EXECUTE FUNCTION taskiem_audit_anchors_append_only();

-- Tenants whose chain moved since their last anchor, and whose last anchor
-- is older than the interval (or who have none): what the anchoring job
-- works on. Routing columns only.
-- +goose StatementBegin
CREATE FUNCTION taskiem_audit_heads_due(p_interval interval)
RETURNS TABLE (tenant_id uuid, chain_seq bigint, head_hash bytea)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT h.tenant_id, h.chain_seq, h.head_hash FROM audit_chain_heads h
   WHERE NOT EXISTS (SELECT 1 FROM audit_anchors a WHERE a.tenant_id = h.tenant_id
                       AND (a.chain_seq = h.chain_seq OR a.anchored_at > now() - p_interval))
$$;
-- +goose StatementEnd
CREATE POLICY dispatch_heads ON audit_chain_heads FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_anchors ON audit_anchors FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, chain_seq, head_hash) ON audit_chain_heads TO taskiem_dispatch;
GRANT SELECT (tenant_id, chain_seq, anchored_at) ON audit_anchors TO taskiem_dispatch;
ALTER FUNCTION taskiem_audit_heads_due(interval) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_audit_heads_due(interval) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_audit_heads_due(interval) TO taskiem_app;

-- Run payload retention when a workflow sets none (spec 9.4).
ALTER TABLE governance_settings ADD COLUMN default_retention text;

-- +goose Down
ALTER TABLE governance_settings DROP COLUMN default_retention;
DROP FUNCTION taskiem_audit_heads_due(interval);
DROP POLICY dispatch_heads ON audit_chain_heads;
DROP TABLE audit_anchors;
DROP FUNCTION taskiem_audit_anchors_append_only();
