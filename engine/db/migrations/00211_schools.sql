-- The Schools area and its record (decision 0046; docs/spec/schools.md 4,
-- B2; docs/schools.md): classes, sessions and terms, students, families
-- and their guardians, and enrollments, for a business that turned the
-- area on. The record is changed through the API (engine/schools) and
-- read by workflows through schools@1; its kept copies in data tables are
-- read-only.
--
-- The rules this schema keeps:
--   - a class's name and arm are unique in a school; an admission number
--     too (whatever its case);
--   - one current term per school; a past term (started, no longer
--     current) and its enrollments are never changed;
--   - a student is enrolled in a term once;
--   - a guardian belongs to one family per school: their phone's
--     personal-data subject (a pseudonym) is unique;
--   - a session is promoted once.
-- Students' and guardians' details are sealed (name, date of birth;
-- name, phone, email, relationship) under their own subjects, so erasing
-- one erases them.
-- +goose Up
CREATE TABLE school_settings (
  tenant_id   uuid PRIMARY KEY REFERENCES tenants(id),
  enabled     boolean NOT NULL DEFAULT false,
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE school_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON school_settings TO taskiem_app;

CREATE TABLE school_classes (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  name         text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
  arm          text NOT NULL DEFAULT '' CHECK (length(arm) <= 20),
  class_order  int NOT NULL CHECK (class_order BETWEEN 0 AND 1000),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  deleted_at   timestamptz,               -- removed before any student was in it
  mirrored_at  timestamptz                -- the updated_at last copied to the kept table
);
CREATE UNIQUE INDEX school_classes_name ON school_classes (tenant_id, lower(name), lower(arm)) WHERE deleted_at IS NULL;
ALTER TABLE school_classes ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_classes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_classes TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_classes TO taskiem_app;
GRANT UPDATE (name, arm, class_order, updated_at, deleted_at, mirrored_at) ON school_classes TO taskiem_app;

CREATE TABLE school_sessions (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  label       text NOT NULL CHECK (label ~ '^[0-9]{4}/[0-9]{4}$'),
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, label)
);
ALTER TABLE school_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_sessions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_sessions TO taskiem_app;

CREATE TABLE school_terms (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  session_id   uuid NOT NULL REFERENCES school_sessions(id),
  name         text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 40),
  starts_on    date NOT NULL,
  ends_on      date NOT NULL,
  fees_due_on  date NOT NULL,
  started_at   timestamptz,
  started_by   text,
  current      boolean NOT NULL DEFAULT false,
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CHECK (starts_on < ends_on),
  CHECK (fees_due_on <= ends_on),
  CHECK (NOT current OR started_at IS NOT NULL)
);
CREATE UNIQUE INDEX school_terms_name ON school_terms (session_id, lower(name));
-- One current term per school.
CREATE UNIQUE INDEX school_terms_current ON school_terms (tenant_id) WHERE current;
ALTER TABLE school_terms ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_terms FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_terms TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON school_terms TO taskiem_app;
GRANT UPDATE (name, starts_on, ends_on, fees_due_on, started_at, started_by, current, updated_at) ON school_terms TO taskiem_app;

-- A past term is read-only: once started and no longer current, nothing
-- of it changes, and a started term is never deleted nor un-started.
-- +goose StatementBegin
CREATE FUNCTION taskiem_school_term_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.started_at IS NOT NULL THEN
      RAISE EXCEPTION 'term % has started and cannot be deleted', OLD.name USING ERRCODE = '23514';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.started_at IS NOT NULL AND NOT OLD.current THEN
    RAISE EXCEPTION 'term % is past and read-only', OLD.name USING ERRCODE = '23514';
  END IF;
  IF OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at THEN
    RAISE EXCEPTION 'term % has started', OLD.name USING ERRCODE = '23514';
  END IF;
  IF NEW.session_id IS DISTINCT FROM OLD.session_id THEN
    RAISE EXCEPTION 'a term stays in its session' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER school_term_guard BEFORE UPDATE OR DELETE ON school_terms
  FOR EACH ROW EXECUTE FUNCTION taskiem_school_term_guard();

CREATE TABLE school_families (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE school_families ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_families FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_families TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON school_families TO taskiem_app;
GRANT UPDATE (updated_at) ON school_families TO taskiem_app;

-- A guardian is sealed under their phone's subject (subject_id, the
-- pseudonym engine/secrets derives from the phone): unique per school,
-- so a guardian is in one family.
CREATE TABLE school_guardians (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  family_id   uuid NOT NULL REFERENCES school_families(id),
  subject_id  text NOT NULL CHECK (subject_id LIKE 'phone:%'),
  details     jsonb NOT NULL,              -- sealed: {name, phone, email, relationship}
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, subject_id)
);
CREATE INDEX school_guardians_family ON school_guardians (family_id);
ALTER TABLE school_guardians ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_guardians FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_guardians TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON school_guardians TO taskiem_app;
GRANT UPDATE (details, updated_at) ON school_guardians TO taskiem_app;

-- A student is sealed under a subject of their own (subject_id, derived
-- from the student's id).
CREATE TABLE school_students (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  admission_number  text NOT NULL CHECK (admission_number ~ '^[A-Za-z0-9][A-Za-z0-9/_.-]{0,39}$'),
  subject_id        text NOT NULL,
  details           jsonb NOT NULL,        -- sealed: {name, date_of_birth}
  class_id          uuid NOT NULL REFERENCES school_classes(id),
  status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'withdrawn', 'graduated')),
  family_id         uuid REFERENCES school_families(id),
  created_by        text NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  mirrored_at       timestamptz
);
CREATE UNIQUE INDEX school_students_admission ON school_students (tenant_id, upper(admission_number));
CREATE INDEX school_students_class ON school_students (tenant_id, class_id);
CREATE INDEX school_students_family ON school_students (family_id);
ALTER TABLE school_students ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_students FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_students TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_students TO taskiem_app;
GRANT UPDATE (admission_number, details, class_id, status, family_id, updated_at, mirrored_at) ON school_students TO taskiem_app;

-- A student in a class for a term: one row per student and term.
CREATE TABLE school_enrollments (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  term_id     uuid NOT NULL REFERENCES school_terms(id),
  student_id  uuid NOT NULL REFERENCES school_students(id),
  class_id    uuid NOT NULL REFERENCES school_classes(id),
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (term_id, student_id)
);
CREATE INDEX school_enrollments_student ON school_enrollments (student_id);
ALTER TABLE school_enrollments ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_enrollments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_enrollments TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON school_enrollments TO taskiem_app;
GRANT UPDATE (class_id, updated_at) ON school_enrollments TO taskiem_app;

-- A past term's enrollments are its history: never changed. Enrolling in
-- a term that has not started, or is past, is refused too.
-- +goose StatementBegin
CREATE FUNCTION taskiem_school_enrollment_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  t record;
BEGIN
  SELECT started_at, current, name INTO t FROM school_terms
   WHERE id = CASE WHEN TG_OP = 'DELETE' THEN OLD.term_id ELSE NEW.term_id END;
  IF t.started_at IS NULL OR NOT t.current THEN
    RAISE EXCEPTION 'term % is not current: its enrollments are read-only', t.name USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'UPDATE' AND (NEW.term_id IS DISTINCT FROM OLD.term_id OR NEW.student_id IS DISTINCT FROM OLD.student_id) THEN
    RAISE EXCEPTION 'an enrollment stays the same student''s in the same term' USING ERRCODE = '23514';
  END IF;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER school_enrollment_guard BEFORE INSERT OR UPDATE OR DELETE ON school_enrollments
  FOR EACH ROW EXECUTE FUNCTION taskiem_school_enrollment_guard();

-- A session's promotion, once: who moved up, repeated, left or graduated.
CREATE TABLE school_promotions (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  session_id  uuid NOT NULL REFERENCES school_sessions(id),
  summary     jsonb NOT NULL,
  created_by  text NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, session_id)
);
ALTER TABLE school_promotions ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_promotions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_promotions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_promotions TO taskiem_app;

-- The kept copies (students, classes in data tables): the businesses with
-- the area on and something not yet copied. Names only, across tenants,
-- so the dispatch role's.
-- +goose StatementBegin
CREATE FUNCTION taskiem_schools_unmirrored_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM school_settings s
   WHERE s.enabled
     AND (EXISTS (SELECT 1 FROM school_students x WHERE x.tenant_id = s.tenant_id AND x.mirrored_at IS DISTINCT FROM x.updated_at)
       OR EXISTS (SELECT 1 FROM school_classes c WHERE c.tenant_id = s.tenant_id AND c.mirrored_at IS DISTINCT FROM c.updated_at))
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_schools_unmirrored_tenants() OWNER TO taskiem_dispatch;
CREATE POLICY dispatch ON school_settings FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch ON school_students FOR SELECT TO taskiem_dispatch USING (true);
CREATE POLICY dispatch ON school_classes FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, enabled) ON school_settings TO taskiem_dispatch;
GRANT SELECT (tenant_id, updated_at, mirrored_at) ON school_students TO taskiem_dispatch;
GRANT SELECT (tenant_id, updated_at, mirrored_at) ON school_classes TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_schools_unmirrored_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_schools_unmirrored_tenants() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_schools_unmirrored_tenants();
DROP TABLE school_promotions;
DROP TABLE school_enrollments;
DROP FUNCTION taskiem_school_enrollment_guard();
DROP TABLE school_students;
DROP TABLE school_guardians;
DROP TABLE school_families;
DROP TABLE school_terms;
DROP FUNCTION taskiem_school_term_guard();
DROP TABLE school_sessions;
DROP TABLE school_classes;
DROP TABLE school_settings;
