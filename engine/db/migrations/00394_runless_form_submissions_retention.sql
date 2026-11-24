-- A form submission that starts no run had no retention (F6b): 00380
-- deletes submissions with their run's purge, but a refused submission
-- (status 'refused') or one held at a plan limit (status 'received', a
-- note, no run) has no run, so it, its answers and its files were kept for
-- ever. And files stored before a refusal (a later file failing the
-- malware scan, answers not fitting the form) were left in the
-- organisation's storage with no row naming them.
-- - form_submissions.retain_until: set by the api when a submission ends
--   without a run, to now plus the retention its run would have had
--   (runtime.Store.Retention); cleared when a run starts or is answered.
--   Rows already kept without a run get created_at + 90 days, the default
--   run retention.
-- - taskiem_form_submissions_expire deletes those past retain_until,
--   across businesses, in batches (two api pods racing delete the same
--   rows at most once), and queues their files for removal
--   (form_file_deletions); the api's forms loop calls it
--   (forms.Service.DrainDeletions).
-- - taskiem_form_files_discard queues files the api stored for a
--   submission it then refused or failed to record; it refuses a key a
--   form file row still names, and works only in the caller's tenant.
-- form_file_deletions and the DELETE grant on form_submissions come from
-- 00380, which is numbered after this one but merged with it: the two
-- functions are plpgsql, whose tables are resolved when they run.
-- +goose Up
ALTER TABLE form_submissions ADD COLUMN retain_until timestamptz;
GRANT SELECT (retain_until) ON form_submissions TO taskiem_dispatch;

UPDATE form_submissions SET retain_until = created_at + interval '90 days'
 WHERE run_id IS NULL AND (status = 'refused' OR note IS NOT NULL);

-- +goose StatementBegin
CREATE FUNCTION taskiem_form_submissions_expire(p_limit int)
RETURNS bigint
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_ids uuid[];
BEGIN
  SELECT array_agg(x.id) INTO v_ids FROM (
    SELECT s.id FROM form_submissions s
     WHERE s.run_id IS NULL AND s.retain_until IS NOT NULL AND s.retain_until < now()
     ORDER BY s.retain_until
     LIMIT LEAST(GREATEST(p_limit, 1), 1000)) x;
  IF v_ids IS NULL THEN
    RETURN 0;
  END IF;
  INSERT INTO form_file_deletions (tenant_id, storage_key)
    SELECT f.tenant_id, f.storage_key FROM form_files f WHERE f.submission_id = ANY (v_ids)
    ON CONFLICT DO NOTHING;
  DELETE FROM form_submissions WHERE id = ANY (v_ids);
  RETURN cardinality(v_ids);
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_submissions_expire(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_submissions_expire(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_submissions_expire(int) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_form_files_discard(p_tenant uuid, p_keys text[])
RETURNS void
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF p_tenant IS NULL OR NOT (p_tenant = ANY (taskiem_tenant_scope())) THEN
    RAISE EXCEPTION 'not in scope' USING ERRCODE = '42501';
  END IF;
  INSERT INTO form_file_deletions (tenant_id, storage_key)
    SELECT p_tenant, k FROM unnest(p_keys) k
     WHERE k LIKE 'files/%'
       AND NOT EXISTS (SELECT 1 FROM form_files f WHERE f.tenant_id = p_tenant AND f.storage_key = k)
    ON CONFLICT DO NOTHING;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_files_discard(uuid, text[]) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_files_discard(uuid, text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_files_discard(uuid, text[]) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_form_files_discard(uuid, text[]);
DROP FUNCTION taskiem_form_submissions_expire(int);
REVOKE SELECT (retain_until) ON form_submissions FROM taskiem_dispatch;
ALTER TABLE form_submissions DROP COLUMN retain_until;
