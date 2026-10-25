-- Chasing (decision 0046; docs/spec/schools.md 11, B7; docs/schools.md
-- #debtors): the reminder ladder's wording per school, the reminders
-- sent, promises to pay, the plan grace, each instalment a plan missed,
-- and whether a family was told of its plan's decision.
--
-- school_reminder_sends is one row per send request (the API's
-- Idempotency-Key, a workflow step's key) with what it did, so a retried
-- request answers the same and messages nobody twice. school_reminders is
-- one row per student reminded: the rung, the balance then, and how the
-- family was reached; the last one is what Debtors shows.
--
-- school_promises is a promise to pay by a date, recorded by the bursar:
-- open until the date; the day after, kept when the balance moved, missed
-- when it did not, each decided exactly once by the check that moves it
-- from open; withdrawn by hand. Nothing is deleted.
--
-- school_plan_arrears is each instalment of an approved plan found past
-- due (plus the school's grace) and not covered by what was paid since
-- the plan was approved: inserted once per instalment by the check, which
-- tells the bursar when it inserts.
-- +goose Up
ALTER TABLE school_settings ADD COLUMN reminder_wording jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(reminder_wording) = 'object');
ALTER TABLE school_settings ADD COLUMN plan_grace_days int NOT NULL DEFAULT 0 CHECK (plan_grace_days BETWEEN 0 AND 60);
ALTER TABLE school_plan_requests ADD COLUMN told_at timestamptz;
GRANT UPDATE (told_at) ON school_plan_requests TO taskiem_app;

CREATE TABLE school_reminder_sends (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  key         text NOT NULL CHECK (length(key) BETWEEN 1 AND 200),
  result      jsonb NOT NULL,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, key)
);
ALTER TABLE school_reminder_sends ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_reminder_sends FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_reminder_sends TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_reminder_sends TO taskiem_app;

CREATE TABLE school_reminders (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  student_id    uuid NOT NULL REFERENCES school_students(id),
  family_id     uuid NOT NULL REFERENCES school_families(id),
  rung          int NOT NULL CHECK (rung BETWEEN 1 AND 3),
  balance_kobo  bigint NOT NULL CHECK (balance_kobo >= 0),
  sent_on       date NOT NULL,
  channels      jsonb NOT NULL DEFAULT '{}',   -- {whatsapp: 1, sms: 1}: the guardians reached by channel
  nobody        boolean NOT NULL DEFAULT false, -- no guardian could be reached
  send_key      text NOT NULL,
  created_by    text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX school_reminders_once ON school_reminders (tenant_id, send_key, student_id);
CREATE INDEX school_reminders_student ON school_reminders (tenant_id, student_id, created_at DESC);
CREATE INDEX school_reminders_family_day ON school_reminders (tenant_id, family_id, sent_on, rung);
ALTER TABLE school_reminders ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_reminders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_reminders TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_reminders TO taskiem_app;

CREATE TABLE school_promises (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  student_id    uuid NOT NULL REFERENCES school_students(id),
  family_id     uuid REFERENCES school_families(id),
  promised_on   date NOT NULL,
  note          jsonb NOT NULL,                -- sealed: the bursar's note
  balance_kobo  bigint NOT NULL CHECK (balance_kobo >= 0), -- the balance when it was made
  status        text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'kept', 'missed', 'withdrawn')),
  created_by    text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  resolved_at   timestamptz,
  resolved_by   text,
  CHECK ((status = 'open') = (resolved_at IS NULL))
);
CREATE UNIQUE INDEX school_promises_one_open ON school_promises (student_id) WHERE status = 'open';
CREATE INDEX school_promises_student ON school_promises (tenant_id, student_id, created_at DESC);
CREATE INDEX school_promises_due ON school_promises (tenant_id, promised_on) WHERE status = 'open';
ALTER TABLE school_promises ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_promises FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_promises TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_promises TO taskiem_app;
GRANT UPDATE (status, resolved_at, resolved_by) ON school_promises TO taskiem_app;

CREATE TABLE school_plan_arrears (
  plan_id      uuid NOT NULL REFERENCES school_plan_requests(id),
  instalment   int NOT NULL CHECK (instalment BETWEEN 1 AND 6),
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  student_id   uuid NOT NULL REFERENCES school_students(id),
  due_on       date NOT NULL,
  amount_kobo  bigint NOT NULL,
  short_kobo   bigint NOT NULL,                -- what was still missing when it was marked
  marked_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (plan_id, instalment)
);
CREATE INDEX school_plan_arrears_student ON school_plan_arrears (tenant_id, student_id, marked_at DESC);
ALTER TABLE school_plan_arrears ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_plan_arrears FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_plan_arrears TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_plan_arrears TO taskiem_app;

-- The schools the hourly chasing check visits: every one with the area on.
-- +goose StatementBegin
CREATE FUNCTION taskiem_schools_enabled_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM school_settings s WHERE s.enabled ORDER BY s.tenant_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_schools_enabled_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_schools_enabled_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_schools_enabled_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_schools_enabled_tenants();
DROP TABLE school_plan_arrears;
DROP TABLE school_promises;
DROP TABLE school_reminders;
DROP TABLE school_reminder_sends;
REVOKE UPDATE (told_at) ON school_plan_requests FROM taskiem_app;
ALTER TABLE school_plan_requests DROP COLUMN told_at;
ALTER TABLE school_settings DROP COLUMN plan_grace_days;
ALTER TABLE school_settings DROP COLUMN reminder_wording;
