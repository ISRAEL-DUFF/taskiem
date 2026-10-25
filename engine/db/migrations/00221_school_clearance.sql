-- Clearance and the gate (decision 0046, points 4 and 10; docs/spec/
-- schools.md 12, B8; docs/schools.md#clearance). Clearance itself is
-- computed every time it is read (the term's invoices, the plan's
-- standing, an override in force) and is stored nowhere: there is no
-- clearance column anyone could write.
--
-- school_clearance_overrides is the one thing kept: a person with
-- school.approve clearing a student for a term who does not meet the
-- rule, with a reason (sealed, like a promise's note: it is about the
-- family) and the date it ends (the term's end by default). One override
-- in force per student and term; past its end it no longer counts;
-- withdrawn by hand. Nothing is deleted.
-- +goose Up
CREATE TABLE school_clearance_overrides (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  student_id    uuid NOT NULL REFERENCES school_students(id),
  term_id       uuid NOT NULL REFERENCES school_terms(id),
  reason        jsonb NOT NULL,                -- sealed: why the student is cleared
  starts_on     date NOT NULL,
  ends_on       date NOT NULL,
  created_by    text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  withdrawn_at  timestamptz,
  withdrawn_by  text,
  CHECK (ends_on >= starts_on),
  CHECK ((withdrawn_at IS NULL) = (withdrawn_by IS NULL))
);
CREATE UNIQUE INDEX school_clearance_overrides_one ON school_clearance_overrides (student_id, term_id) WHERE withdrawn_at IS NULL;
CREATE INDEX school_clearance_overrides_term ON school_clearance_overrides (tenant_id, term_id, created_at DESC);
CREATE INDEX school_clearance_overrides_student ON school_clearance_overrides (tenant_id, student_id, created_at DESC);
ALTER TABLE school_clearance_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_clearance_overrides FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_clearance_overrides TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_clearance_overrides TO taskiem_app;
GRANT UPDATE (withdrawn_at, withdrawn_by) ON school_clearance_overrides TO taskiem_app;

-- +goose Down
DROP TABLE school_clearance_overrides;
