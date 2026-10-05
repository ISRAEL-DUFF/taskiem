-- Governance: per-subject keys and the hash-chained audit log. Spec 9.2, 9.4.

-- +goose Up
CREATE TABLE subject_keys (
  tenant_id    uuid NOT NULL,
  subject_id   text NOT NULL,                      -- pseudonymous, e.g. hmac(tenant key, bvn)
  wrapped_key  bytea,                              -- data key wrapped by the tenant KEK; NULL once shredded
  created_at   timestamptz NOT NULL DEFAULT now(),
  shredded_at  timestamptz,
  PRIMARY KEY (tenant_id, subject_id),
  CHECK ((wrapped_key IS NULL) = (shredded_at IS NOT NULL))
);

-- Shredding is one-way: a shredded key can never be restored.
-- +goose StatementBegin
CREATE FUNCTION taskiem_subject_keys_one_way() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.shredded_at IS NOT NULL THEN
    RAISE EXCEPTION 'subject key % is shredded', OLD.subject_id USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER subject_keys_one_way BEFORE UPDATE ON subject_keys
  FOR EACH ROW EXECUTE FUNCTION taskiem_subject_keys_one_way();

CREATE TABLE audit_log (
  id          bigserial PRIMARY KEY,
  tenant_id   uuid NOT NULL,
  chain_seq   bigint NOT NULL CHECK (chain_seq >= 1),
  actor_type  text NOT NULL CHECK (actor_type IN ('user', 'api_key', 'system', 'ai', 'end_user', 'platform_admin')),
  actor_id    text NOT NULL,
  action      text NOT NULL,                       -- workflow.publish, approval.decide, secret.read ...
  target      text NOT NULL,
  detail      jsonb NOT NULL DEFAULT '{}',         -- never raw PII
  prev_hash   bytea NOT NULL CHECK (length(prev_hash) = 32),
  hash        bytea NOT NULL CHECK (length(hash) = 32),
  at          timestamptz NOT NULL,
  UNIQUE (tenant_id, chain_seq)
);

CREATE TABLE audit_chain_heads (
  tenant_id  uuid PRIMARY KEY,
  chain_seq  bigint NOT NULL,
  head_hash  bytea  NOT NULL CHECK (length(head_hash) = 32)
);

-- Canonical form hashed into the chain. The verifier (SQL below, and the CLI
-- in Phase 1) must reproduce it exactly.
-- +goose StatementBegin
CREATE FUNCTION taskiem_audit_canonical(
  p_tenant uuid, p_seq bigint, p_actor_type text, p_actor_id text,
  p_action text, p_target text, p_detail jsonb, p_at timestamptz)
RETURNS bytea
LANGUAGE sql IMMUTABLE AS $$
  SELECT convert_to(jsonb_build_object(
    'v', 1,
    'tenant_id', p_tenant,
    'chain_seq', p_seq,
    'actor_type', p_actor_type,
    'actor_id', p_actor_id,
    'action', p_action,
    'target', p_target,
    'detail', p_detail,
    'at', to_char(p_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
  )::text, 'UTF8')
$$;
-- +goose StatementEnd

-- Appends to a tenant's chain, serialised by the chain-head row lock.
-- Runs as the caller: RLS requires the tenant to be in scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_audit_append(
  p_tenant uuid, p_actor_type text, p_actor_id text, p_action text, p_target text, p_detail jsonb)
RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE
  v_seq bigint;
  v_prev bytea;
  v_hash bytea;
  v_at timestamptz := date_trunc('microseconds', clock_timestamp());
BEGIN
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

-- Returns the first chain_seq whose hash or link does not verify, or NULL if
-- the tenant's chain is intact up to its head.
-- +goose StatementBegin
CREATE FUNCTION taskiem_audit_verify(p_tenant uuid)
RETURNS bigint
LANGUAGE plpgsql STABLE AS $$
DECLARE
  r record;
  v_prev bytea := '\x0000000000000000000000000000000000000000000000000000000000000000';
  v_expect bigint := 1;
  v_head audit_chain_heads%ROWTYPE;
BEGIN
  FOR r IN SELECT * FROM audit_log WHERE tenant_id = p_tenant ORDER BY chain_seq LOOP
    IF r.chain_seq <> v_expect OR r.prev_hash <> v_prev
       OR r.hash <> sha256(v_prev || taskiem_audit_canonical(
            r.tenant_id, r.chain_seq, r.actor_type, r.actor_id, r.action, r.target, r.detail, r.at)) THEN
      RETURN v_expect;
    END IF;
    v_prev := r.hash;
    v_expect := v_expect + 1;
  END LOOP;
  SELECT * INTO v_head FROM audit_chain_heads WHERE tenant_id = p_tenant;
  IF FOUND AND (v_head.chain_seq <> v_expect - 1 OR v_head.head_hash <> v_prev) THEN
    RETURN v_expect;                               -- rows missing from the end
  END IF;
  RETURN NULL;
END
$$;
-- +goose StatementEnd

ALTER TABLE subject_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE subject_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subject_keys TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_log TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

ALTER TABLE audit_chain_heads ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_chain_heads FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit_chain_heads TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));

GRANT SELECT, INSERT, UPDATE ON subject_keys TO taskiem_app;
-- audit_log is append-only (spec 5.3).
GRANT SELECT, INSERT ON audit_log TO taskiem_app;
GRANT USAGE ON SEQUENCE audit_log_id_seq TO taskiem_app;
GRANT SELECT, INSERT, UPDATE ON audit_chain_heads TO taskiem_app;
