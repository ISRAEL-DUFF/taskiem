-- Onboarding (Phase 4, P4-2; docs/onboarding.md): verified emails, the
-- per-tenant onboarding record that measures signup to first successful
-- run (gate G4), single-use email verification links, and a signup count
-- per client address that holds across API replicas.

-- +goose Up
-- When the person proved they read this address (a verification link).
-- The dispatch role's column grant on users is unchanged: it never reads it.
ALTER TABLE users ADD COLUMN email_verified_at timestamptz;

-- One row per tenant that signed itself up (source 'signup'), or that
-- dismissed its checklist (source 'existing'). first_run_at is set once,
-- when the tenant's first run completes (engine/runtime, endRun).
CREATE TABLE tenant_onboarding (
  tenant_id     uuid PRIMARY KEY REFERENCES tenants(id),
  source        text NOT NULL CHECK (source IN ('signup', 'existing')),
  signed_up_by  uuid REFERENCES users(id),
  signed_up_at  timestamptz NOT NULL DEFAULT now(),
  first_run_at  timestamptz,
  first_run_id  uuid,
  dismissed_at  timestamptz,
  CHECK ((first_run_at IS NULL) = (first_run_id IS NULL))
);
ALTER TABLE tenant_onboarding ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_onboarding FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_onboarding TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON tenant_onboarding TO taskiem_app;

-- An email verification link: a selector, to find the row, and a 256-bit
-- secret kept only as its SHA-256 (as password resets). It works once,
-- for a day, by the person it was sent to, signed in to the tenant they
-- signed up; not after five wrong secrets. Asking again replaces any
-- unused link.
CREATE TABLE email_verifications (
  selector     bytea PRIMARY KEY CHECK (length(selector) = 16),
  secret_hash  bytea NOT NULL CHECK (length(secret_hash) = 32),
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  user_id      uuid NOT NULL REFERENCES users(id),
  email        text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  used_at      timestamptz,
  failures     int NOT NULL DEFAULT 0
);
CREATE INDEX ON email_verifications (tenant_id, user_id);
ALTER TABLE email_verifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_verifications FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON email_verifications TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON email_verifications TO taskiem_app;

-- Signups per client address, for a limit every API replica shares. The
-- address is kept only as a SHA-256; rows older than a day are removed as
-- new ones come. Reached only through taskiem_signup_admit.
CREATE TABLE signup_attempts (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  addr_hash   bytea NOT NULL CHECK (length(addr_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON signup_attempts (addr_hash, created_at);
CREATE INDEX ON signup_attempts (created_at);
ALTER TABLE signup_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE signup_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON signup_attempts TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON signup_attempts TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON signup_attempts FROM PUBLIC;
GRANT SELECT, INSERT, DELETE ON signup_attempts TO taskiem_dispatch;

-- Admits a signup from an address if it made fewer than p_max in the last
-- p_window, and counts it; false otherwise (and not counted). Serialised
-- per address, so concurrent signups cannot both take the last place.
-- +goose StatementBegin
CREATE FUNCTION taskiem_signup_admit(p_addr bytea, p_window interval, p_max int)
RETURNS boolean
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended('taskiem_signup_admit:' || encode(p_addr, 'hex'), 0));
  DELETE FROM signup_attempts WHERE created_at < now() - interval '1 day' - p_window;
  IF (SELECT count(*) FROM signup_attempts a WHERE a.addr_hash = p_addr AND a.created_at > now() - p_window) >= p_max THEN
    RETURN false;
  END IF;
  INSERT INTO signup_attempts (addr_hash) VALUES (p_addr);
  RETURN true;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_signup_admit(bytea, interval, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_signup_admit(bytea, interval, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_signup_admit(bytea, interval, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_signup_admit(bytea, interval, int);
DROP TABLE signup_attempts;
DROP TABLE email_verifications;
DROP TABLE tenant_onboarding;
ALTER TABLE users DROP COLUMN email_verified_at;
