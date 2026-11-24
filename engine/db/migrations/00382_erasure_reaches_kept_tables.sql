-- Erasure reaches the kept tables (school_students, school_term_fees and
-- money_log in the business's Xenibase project). Those hold the opened
-- name (and for money_log the payer's account) in clear, and a row was
-- copied again only when its source changed; erasure changed only
-- subject_keys, so the clear copies stayed for good.
-- - Erasing a student's subject moves the student's updated_at, and their
--   term invoices', so the schools mirror copies them again, with the name
--   it now reads: "(erased)".
-- - An entry of the money log is never changed (taskiem_money_entry_kept),
--   so erasing a payer queues the entries that name them in
--   money_log_erasures; the money log's mirror replaces the kept row's
--   payer with "[erased]" and takes the entry off the queue.
--   taskiem_money_unmirrored_tenants lists businesses with such work too.
-- The trigger function is owned by taskiem_dispatch and reads only the
-- columns granted here. The indexes it looks up by are 00383's.
-- +goose Up
CREATE TABLE money_log_erasures (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  entry_id   uuid NOT NULL REFERENCES money_entries(id),
  queued_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, entry_id)
);
ALTER TABLE money_log_erasures ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_log_erasures FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_log_erasures TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, DELETE ON money_log_erasures TO taskiem_app;
CREATE POLICY dispatch ON money_log_erasures TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (tenant_id), INSERT ON money_log_erasures TO taskiem_dispatch;

CREATE POLICY dispatch_erased ON school_students FOR UPDATE TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (subject_id), UPDATE (updated_at) ON school_students TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_kept_copies_erased() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  UPDATE customer_invoices i SET updated_at = now()
    FROM school_term_invoices ti JOIN school_students s ON s.id = ti.student_id
   WHERE s.tenant_id = NEW.tenant_id AND s.subject_id = NEW.subject_id
     AND ti.tenant_id = NEW.tenant_id AND i.id = ti.invoice_id;
  UPDATE school_students SET updated_at = now() WHERE tenant_id = NEW.tenant_id AND subject_id = NEW.subject_id;
  INSERT INTO money_log_erasures (tenant_id, entry_id)
    SELECT e.tenant_id, e.id FROM money_entries e WHERE e.tenant_id = NEW.tenant_id AND e.payer->>'subject' = NEW.subject_id
    ON CONFLICT DO NOTHING;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_kept_copies_erased() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_kept_copies_erased() FROM PUBLIC;
CREATE TRIGGER kept_copies_erased AFTER UPDATE OF shredded_at ON subject_keys
  FOR EACH ROW WHEN (OLD.shredded_at IS NULL AND NEW.shredded_at IS NOT NULL) EXECUTE FUNCTION taskiem_kept_copies_erased();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_unmirrored_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT e.tenant_id FROM money_entries e WHERE e.mirrored_at IS NULL
  UNION SELECT x.tenant_id FROM money_log_erasures x
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_unmirrored_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT e.tenant_id FROM money_entries e WHERE e.mirrored_at IS NULL
$$;
-- +goose StatementEnd
DROP TRIGGER kept_copies_erased ON subject_keys;
DROP FUNCTION taskiem_kept_copies_erased();
REVOKE SELECT (subject_id), UPDATE (updated_at) ON school_students FROM taskiem_dispatch;
DROP POLICY dispatch_erased ON school_students;
DROP TABLE money_log_erasures;
