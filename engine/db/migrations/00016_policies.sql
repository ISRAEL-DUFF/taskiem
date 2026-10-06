-- Approval policies, delegation and step-up (spec 9.1).

-- +goose Up
-- Versions of a tenant's approval policies. One version per name is
-- active; a new version may wait for a second person's approval (four-eyes
-- on policy edits) before it becomes active. Runs snapshot active versions
-- when they start.
CREATE TABLE approval_policies (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  name         text NOT NULL,
  version      int  NOT NULL CHECK (version >= 1),
  document     jsonb NOT NULL,
  state        text NOT NULL CHECK (state IN ('pending', 'active', 'superseded', 'rejected')),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  decided_by   text,
  decided_at   timestamptz,
  PRIMARY KEY (tenant_id, name, version)
);
CREATE UNIQUE INDEX approval_policies_active ON approval_policies (tenant_id, name) WHERE state = 'active';

-- Time-boxed delegation of approval roles from one member to another.
CREATE TABLE delegations (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  from_user   uuid NOT NULL REFERENCES users(id),
  to_user     uuid NOT NULL REFERENCES users(id),
  roles       text[] NOT NULL CHECK (cardinality(roles) > 0),
  starts_at   timestamptz NOT NULL,
  ends_at     timestamptz NOT NULL,
  reason      text NOT NULL,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  revoked_at  timestamptz,
  CHECK (ends_at > starts_at),
  CHECK (from_user <> to_user)
);
CREATE INDEX delegations_to ON delegations (tenant_id, to_user, ends_at) WHERE revoked_at IS NULL;

-- Multi-level approvals: the levels a policy chose, and the current one.
ALTER TABLE approvals
  ADD COLUMN levels         jsonb,
  ADD COLUMN level          int NOT NULL DEFAULT 0,
  ADD COLUMN step_up        text,
  ADD COLUMN constraints    jsonb,
  ADD COLUMN policy_version int;

-- A decision records its level, the step-up the approver passed, and whom
-- they acted for under a delegation. One decision per person per level.
ALTER TABLE approval_decisions
  ADD COLUMN level        int NOT NULL DEFAULT 0,
  ADD COLUMN step_up      text,
  ADD COLUMN on_behalf_of uuid REFERENCES users(id);
ALTER TABLE approval_decisions DROP CONSTRAINT approval_decisions_pkey;
ALTER TABLE approval_decisions ADD PRIMARY KEY (run_id, step_id, level, user_id);

-- Step-up enrolment per member. The TOTP secret itself is an encrypted
-- tenant secret; this holds its state and the last time step used, so a
-- code cannot be replayed.
CREATE TABLE member_mfa (
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  user_id            uuid NOT NULL REFERENCES users(id),
  totp_confirmed_at  timestamptz,
  totp_last_step     bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, user_id)
);

-- Four-eyes on change (spec 9.1): whether publishing and policy edits need
-- a second person. Off until a tenant's owner turns them on.
CREATE TABLE governance_settings (
  tenant_id           uuid PRIMARY KEY REFERENCES tenants(id),
  four_eyes_publish   boolean NOT NULL DEFAULT false,
  four_eyes_policies  boolean NOT NULL DEFAULT false,
  updated_by          text NOT NULL,
  updated_at          timestamptz NOT NULL DEFAULT now()
);

-- A publish waiting for a second person.
CREATE TABLE publish_requests (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  workflow_id   uuid NOT NULL,
  version       int  NOT NULL,
  requested_by  uuid NOT NULL,
  requested_at  timestamptz NOT NULL DEFAULT now(),
  status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'withdrawn')),
  decided_by    uuid,
  decided_at    timestamptz,
  comment       text,
  PRIMARY KEY (workflow_id, version),
  FOREIGN KEY (workflow_id, version) REFERENCES workflow_versions (workflow_id, version)
);
CREATE INDEX publish_requests_pending ON publish_requests (tenant_id) WHERE status = 'pending';

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['approval_policies', 'delegations', 'member_mfa', 'governance_settings', 'publish_requests'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE publish_requests;
DROP TABLE governance_settings;
DROP TABLE member_mfa;
ALTER TABLE approval_decisions DROP CONSTRAINT approval_decisions_pkey;
ALTER TABLE approval_decisions ADD PRIMARY KEY (run_id, step_id, user_id);
ALTER TABLE approval_decisions DROP COLUMN on_behalf_of, DROP COLUMN step_up, DROP COLUMN level;
ALTER TABLE approvals DROP COLUMN policy_version, DROP COLUMN constraints, DROP COLUMN step_up, DROP COLUMN level, DROP COLUMN levels;
DROP TABLE delegations;
DROP TABLE approval_policies;
