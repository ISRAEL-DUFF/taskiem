-- Hosted forms' data follows retention and erasure (docs/spec/forms.md,
-- "Retention" and "Erasure"). Until now nothing deleted form_submissions
-- or form_files, and the files' bytes stayed in the organisation's
-- Xenibase storage: a respondent's uploaded ID outlived both the run's
-- retention and their erasure, and still signed a download link.
-- - taskiem_purge_run, as 00012 left it, now also deletes the run's form
--   submissions (form_files cascades) and queues their stored files.
-- - Erasing a subject (subject_keys.shredded_at set) deletes the tenant's
--   submissions whose answers or respondent hold an envelope of that
--   subject, and queues their files. Files are not sealed, so destroying
--   a key alone cannot erase them.
-- - form_file_deletions is the queue of stored objects to remove; the api
--   drains it through media.Service.Delete (forms.Service.DrainDeletions):
--   taskiem_form_file_deletions_claim leases a batch across businesses,
--   taskiem_form_file_deletions_done removes one once its object is gone.
-- The functions are owned by taskiem_dispatch, which reads only the
-- columns granted here.
-- +goose Up
CREATE TABLE form_file_deletions (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  storage_key   text NOT NULL,
  queued_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until timestamptz,
  PRIMARY KEY (tenant_id, storage_key)
);
CREATE INDEX form_file_deletions_queue ON form_file_deletions (queued_at);
ALTER TABLE form_file_deletions ENABLE ROW LEVEL SECURITY;
ALTER TABLE form_file_deletions FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON form_file_deletions TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON form_file_deletions TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON form_file_deletions TO taskiem_dispatch;

-- What the purge and the erasure trigger read and delete.
CREATE POLICY dispatch_forget ON form_submissions FOR DELETE TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, run_id, answers, respondent), DELETE ON form_submissions TO taskiem_dispatch;
CREATE POLICY dispatch ON form_files FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, submission_id, storage_key) ON form_files TO taskiem_dispatch;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_purge_run(p_run uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_ok boolean;
BEGIN
  SELECT true INTO v_ok FROM runs
   WHERE id = p_run AND tenant_id = ANY (taskiem_tenant_scope())
     AND status IN ('completed', 'failed', 'cancelled') AND retain_until IS NOT NULL AND retain_until < now()
   FOR UPDATE;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  -- The run's form submissions and files (their bytes once the api has
  -- removed them from storage).
  INSERT INTO form_file_deletions (tenant_id, storage_key)
    SELECT f.tenant_id, f.storage_key FROM form_files f JOIN form_submissions s ON s.id = f.submission_id
     WHERE s.run_id = p_run
    ON CONFLICT DO NOTHING;
  DELETE FROM form_submissions WHERE run_id = p_run;
  DELETE FROM run_events WHERE run_id = p_run;
  DELETE FROM tasks WHERE run_id = p_run;
  DELETE FROM timers WHERE run_id = p_run;
  DELETE FROM signal_waits WHERE run_id = p_run;
  DELETE FROM concurrency_slots WHERE run_id = p_run;
  DELETE FROM approval_decisions WHERE run_id = p_run;
  DELETE FROM approvals WHERE run_id = p_run;
  DELETE FROM runs WHERE id = p_run;
  RETURN true;
END
$$;
-- +goose StatementEnd

-- A respondent erased loses their submissions and files.
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_submissions_erased() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_ids uuid[];
BEGIN
  SELECT array_agg(s.id) INTO v_ids FROM form_submissions s
   WHERE s.tenant_id = NEW.tenant_id
     AND (jsonb_path_exists(s.respondent, '$.**.subject ? (@ == $s)', jsonb_build_object('s', NEW.subject_id))
          OR jsonb_path_exists(s.answers, '$.**.subject ? (@ == $s)', jsonb_build_object('s', NEW.subject_id)));
  IF v_ids IS NULL THEN
    RETURN NEW;
  END IF;
  INSERT INTO form_file_deletions (tenant_id, storage_key)
    SELECT f.tenant_id, f.storage_key FROM form_files f WHERE f.submission_id = ANY (v_ids)
    ON CONFLICT DO NOTHING;
  DELETE FROM form_submissions WHERE id = ANY (v_ids);
  RETURN NEW;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_submissions_erased() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_submissions_erased() FROM PUBLIC;
CREATE TRIGGER form_submissions_erased AFTER UPDATE OF shredded_at ON subject_keys
  FOR EACH ROW WHEN (OLD.shredded_at IS NULL AND NEW.shredded_at IS NOT NULL) EXECUTE FUNCTION taskiem_form_submissions_erased();

-- The api's side of the queue: a leased batch, across businesses (tenant
-- ids and storage keys only), and one removed once its object is gone.
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_file_deletions_claim(p_limit int)
RETURNS TABLE (tenant_id uuid, storage_key text)
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE form_file_deletions d SET claimed_until = now() + interval '5 minutes'
   WHERE (d.tenant_id, d.storage_key) IN (
     SELECT x.tenant_id, x.storage_key FROM form_file_deletions x
      WHERE x.claimed_until IS NULL OR x.claimed_until < now()
      ORDER BY x.queued_at LIMIT LEAST(GREATEST(p_limit, 1), 500) FOR UPDATE SKIP LOCKED)
  RETURNING d.tenant_id, d.storage_key
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_file_deletions_claim(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_file_deletions_claim(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_file_deletions_claim(int) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_form_file_deletions_done(p_tenant uuid, p_key text)
RETURNS void
LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM form_file_deletions WHERE tenant_id = p_tenant AND storage_key = p_key
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_file_deletions_done(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_file_deletions_done(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_file_deletions_done(uuid, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_form_file_deletions_done(uuid, text);
DROP FUNCTION taskiem_form_file_deletions_claim(int);
DROP TRIGGER form_submissions_erased ON subject_keys;
DROP FUNCTION taskiem_form_submissions_erased();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_purge_run(p_run uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_ok boolean;
BEGIN
  SELECT true INTO v_ok FROM runs
   WHERE id = p_run AND tenant_id = ANY (taskiem_tenant_scope())
     AND status IN ('completed', 'failed', 'cancelled') AND retain_until IS NOT NULL AND retain_until < now()
   FOR UPDATE;
  IF NOT FOUND THEN
    RETURN false;
  END IF;
  DELETE FROM run_events WHERE run_id = p_run;
  DELETE FROM tasks WHERE run_id = p_run;
  DELETE FROM timers WHERE run_id = p_run;
  DELETE FROM signal_waits WHERE run_id = p_run;
  DELETE FROM concurrency_slots WHERE run_id = p_run;
  DELETE FROM approval_decisions WHERE run_id = p_run;
  DELETE FROM approvals WHERE run_id = p_run;
  DELETE FROM runs WHERE id = p_run;
  RETURN true;
END
$$;
-- +goose StatementEnd
REVOKE SELECT (id, tenant_id, run_id, answers, respondent), DELETE ON form_submissions FROM taskiem_dispatch;
DROP POLICY dispatch_forget ON form_submissions;
DROP POLICY dispatch ON form_files;
REVOKE SELECT (tenant_id, submission_id, storage_key) ON form_files FROM taskiem_dispatch;
DROP TABLE form_file_deletions;
