-- +goose NO TRANSACTION
-- Erasing a subject runs 00382's trigger inside the erasing transaction:
-- it finds a business's students by subject_id and its money entries by
-- their payer's subject. Without these indexes each erasure read every
-- one of the business's students and entries. money_entries is large, so
-- both are built CONCURRENTLY (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY school_students_subject ON school_students (tenant_id, subject_id);
CREATE INDEX CONCURRENTLY money_entries_payer_subject ON money_entries (tenant_id, (payer->>'subject')) WHERE payer IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS money_entries_payer_subject;
DROP INDEX CONCURRENTLY IF EXISTS school_students_subject;
