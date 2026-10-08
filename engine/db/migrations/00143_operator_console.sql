-- Reads for the operator console (decision 0027), and the review
-- checklist enforced where four eyes already is.
--
-- The tenant list shows what the operator CLI already shows about a
-- tenant (its id and name, status, plan and subscription state, worker
-- pool), from the columns taskiem_dispatch may already read: nothing about
-- members, workflows, runs or secrets. Limits and usage come per tenant
-- through the same store functions as `taskiem tenants limits`.
--
-- Approving a catalogue submission now needs every item of the review
-- checklist confirmed in the database, not only in the CLI, so a console
-- (or any other caller) cannot approve without them.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_tenants(p_query text, p_limit int)
RETURNS TABLE (id uuid, name text, parent_id uuid, status text, created_at timestamptz, plan_id text, subscription_status text, pool text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, t.name, t.parent_id, t.status, t.created_at, s.plan_id, s.status, taskiem_tenant_worker_pool(t.id)
    FROM tenants t LEFT JOIN subscriptions s ON s.tenant_id = t.id
   WHERE COALESCE(p_query, '') = '' OR t.id::text = lower(p_query) OR t.name ILIKE '%' || p_query || '%'
   ORDER BY t.created_at DESC, t.id
   LIMIT LEAST(GREATEST(COALESCE(p_limit, 100), 1), 500)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_tenants(text, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_tenants(text, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_tenants(text, int) TO taskiem_app;

-- A submission's history, for the reviewer.
-- +goose StatementBegin
CREATE FUNCTION taskiem_catalogue_history(p_id uuid)
RETURNS TABLE (event text, actor text, note text, at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT event, actor, note, at FROM catalogue_events WHERE version_id = p_id ORDER BY id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_catalogue_history(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_catalogue_history(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_catalogue_history(uuid) TO taskiem_app;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_catalogue_review(p_id uuid, p_reviewer text, p_approve boolean, p_note text, p_checklist jsonb)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v catalogue_versions%ROWTYPE;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM catalogue_reviewers r WHERE r.email = lower(p_reviewer) AND r.removed_at IS NULL) THEN
    RAISE EXCEPTION '% is not a catalogue reviewer', p_reviewer USING ERRCODE = '42501';
  END IF;
  SELECT * INTO v FROM catalogue_versions WHERE id = p_id FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no submission %', p_id USING ERRCODE = 'P0002';
  END IF;
  IF v.state <> 'in_review' THEN
    RAISE EXCEPTION 'submission % is %, not in review', p_id, v.state USING ERRCODE = '55000';
  END IF;
  IF lower(p_reviewer) = lower(v.submitted_by)
     OR EXISTS (SELECT 1 FROM users u WHERE u.id::text = v.submitted_by AND lower(u.email) = lower(p_reviewer))
     OR EXISTS (SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
                 WHERE m.tenant_id = v.publisher_tenant AND lower(u.email) = lower(p_reviewer)) THEN
    RAISE EXCEPTION '% belongs to the publisher: someone outside it must review (four eyes)', p_reviewer USING ERRCODE = '42501';
  END IF;
  IF COALESCE(trim(p_note), '') = '' THEN
    RAISE EXCEPTION 'a review needs a note' USING ERRCODE = '22023';
  END IF;
  IF p_approve AND NOT (COALESCE(p_checklist, '{}'::jsonb) @> '{"identity": true, "classes": true, "hosts": true, "pii": true, "credentials": true, "conformance": true, "licence": true, "docs": true}'::jsonb) THEN
    RAISE EXCEPTION 'to approve, confirm every checklist item: identity, classes, hosts, pii, credentials, conformance, licence, docs' USING ERRCODE = '22023';
  END IF;
  UPDATE catalogue_versions SET state = CASE WHEN p_approve THEN 'approved' ELSE 'rejected' END,
         reviewed_by = lower(p_reviewer), reviewed_at = now(), review_note = p_note
   WHERE id = p_id;
  INSERT INTO catalogue_events (version_id, publisher_tenant, event, actor, note, detail)
  VALUES (p_id, v.publisher_tenant, CASE WHEN p_approve THEN 'approved' ELSE 'rejected' END, 'reviewer:' || lower(p_reviewer), p_note,
          jsonb_build_object('checklist', COALESCE(p_checklist, '{}'::jsonb)));
  RETURN v.publisher_tenant;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_catalogue_review(p_id uuid, p_reviewer text, p_approve boolean, p_note text, p_checklist jsonb)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v catalogue_versions%ROWTYPE;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM catalogue_reviewers r WHERE r.email = lower(p_reviewer) AND r.removed_at IS NULL) THEN
    RAISE EXCEPTION '% is not a catalogue reviewer', p_reviewer USING ERRCODE = '42501';
  END IF;
  SELECT * INTO v FROM catalogue_versions WHERE id = p_id FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'no submission %', p_id USING ERRCODE = 'P0002';
  END IF;
  IF v.state <> 'in_review' THEN
    RAISE EXCEPTION 'submission % is %, not in review', p_id, v.state USING ERRCODE = '55000';
  END IF;
  IF lower(p_reviewer) = lower(v.submitted_by)
     OR EXISTS (SELECT 1 FROM users u WHERE u.id::text = v.submitted_by AND lower(u.email) = lower(p_reviewer))
     OR EXISTS (SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
                 WHERE m.tenant_id = v.publisher_tenant AND lower(u.email) = lower(p_reviewer)) THEN
    RAISE EXCEPTION '% belongs to the publisher: someone outside it must review (four eyes)', p_reviewer USING ERRCODE = '42501';
  END IF;
  IF COALESCE(trim(p_note), '') = '' THEN
    RAISE EXCEPTION 'a review needs a note' USING ERRCODE = '22023';
  END IF;
  UPDATE catalogue_versions SET state = CASE WHEN p_approve THEN 'approved' ELSE 'rejected' END,
         reviewed_by = lower(p_reviewer), reviewed_at = now(), review_note = p_note
   WHERE id = p_id;
  INSERT INTO catalogue_events (version_id, publisher_tenant, event, actor, note, detail)
  VALUES (p_id, v.publisher_tenant, CASE WHEN p_approve THEN 'approved' ELSE 'rejected' END, 'reviewer:' || lower(p_reviewer), p_note,
          jsonb_build_object('checklist', COALESCE(p_checklist, '{}'::jsonb)));
  RETURN v.publisher_tenant;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_catalogue_history(uuid);
DROP FUNCTION taskiem_ops_tenants(text, int);
