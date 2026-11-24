-- The form result page (/f/{id}/done/{ref}) shows a submission's status
-- and its workflow's message under the form named in the address. The
-- status now says which form the submission was sent to, so the page can
-- refuse another form's submission rather than show one business's
-- message under another's name and look (F30). The return type changes,
-- so the function is dropped and created again with the same signature,
-- owner and grant.

-- +goose Up
DROP FUNCTION taskiem_public_form_status(text, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_status(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, message text, created_at timestamptz, endpoint_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.status, s.message, s.created_at, s.endpoint_id FROM form_submissions s WHERE s.reference = p_reference AND s.status_hash = p_status_hash
  UNION ALL
  SELECT 'received', NULL, i.created_at, i.endpoint_id FROM form_inbox i
   WHERE i.reference = p_reference AND i.status_hash = p_status_hash
     AND NOT EXISTS (SELECT 1 FROM form_submissions s WHERE s.reference = p_reference)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_form_status(text, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_status(text, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_status(text, bytea) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_form_status(text, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_status(p_reference text, p_status_hash bytea)
RETURNS TABLE (status text, message text, created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.status, s.message, s.created_at FROM form_submissions s WHERE s.reference = p_reference AND s.status_hash = p_status_hash
  UNION ALL
  SELECT 'received', NULL, i.created_at FROM form_inbox i
   WHERE i.reference = p_reference AND i.status_hash = p_status_hash
     AND NOT EXISTS (SELECT 1 FROM form_submissions s WHERE s.reference = p_reference)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_form_status(text, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_status(text, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_status(text, bytea) TO taskiem_public;
