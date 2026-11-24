-- Clocking in and out (docs/spec/chat-five-more-flows.md, phase 3 plan,
-- step 3): a row a shift, from "in" to "out" (said in chat, a handover, or
-- closed by Taskiem when left open), with the place WhatsApp shared when
-- the person shared one. And what Taskiem has already told about staff
-- (a late clock-in, later a task's nudge), so each is told once.

-- +goose Up
CREATE TABLE staff_clock (
  id         uuid PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  user_id    uuid NOT NULL REFERENCES users(id),
  in_at      timestamptz NOT NULL,
  out_at     timestamptz,
  in_how     text NOT NULL CHECK (in_how IN ('chat', 'web')),
  out_how    text CHECK (out_how IN ('chat', 'web', 'handover', 'auto')),
  -- A maps link of where they were when they clocked in, when they shared it.
  in_where   text CHECK (in_where IS NULL OR length(in_where) <= 200),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((out_at IS NULL) = (out_how IS NULL)),
  CHECK (out_at IS NULL OR out_at >= in_at)
);
-- One open shift a person a business.
CREATE UNIQUE INDEX staff_clock_open ON staff_clock (tenant_id, user_id) WHERE out_at IS NULL;
CREATE INDEX staff_clock_day ON staff_clock (tenant_id, in_at);
ALTER TABLE staff_clock ENABLE ROW LEVEL SECURITY;
ALTER TABLE staff_clock FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_clock TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON staff_clock TO taskiem_app;

CREATE TABLE staff_notices (
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  key       text NOT NULL CHECK (length(key) BETWEEN 1 AND 200),
  sent_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, key)
);
ALTER TABLE staff_notices ENABLE ROW LEVEL SECURITY;
ALTER TABLE staff_notices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_notices TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON staff_notices TO taskiem_app;

-- The businesses the staff job has work in: someone with usual hours, or
-- a shift left open. It reads no one's hours, pay or times.
CREATE POLICY dispatch ON staff_profiles FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, rota) ON staff_profiles TO taskiem_dispatch;
CREATE POLICY dispatch ON staff_clock FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, out_at) ON staff_clock TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_staff_tenants()
RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM staff_profiles WHERE rota <> '[]'::jsonb
  UNION
  SELECT tenant_id FROM staff_clock WHERE out_at IS NULL
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_staff_tenants() OWNER TO taskiem_dispatch;
REVOKE ALL ON FUNCTION taskiem_staff_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_staff_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_staff_tenants();
REVOKE SELECT (tenant_id, out_at) ON staff_clock FROM taskiem_dispatch;
REVOKE SELECT (tenant_id, rota) ON staff_profiles FROM taskiem_dispatch;
DROP POLICY dispatch ON staff_profiles;
DROP TABLE staff_notices;
DROP TABLE staff_clock;
