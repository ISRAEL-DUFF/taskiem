-- One customer across the app and WhatsApp, review fixes
-- (docs/spec/taskiem-for-everyone.md §11, §5.5 item 4; "As built: stage 4,
-- server review fixes").
--
--   * The sweeps' definers answer across every business, so they are for
--     the sweep alone: refused inside any business's or person's
--     transaction (a request's), as the platform's template rows are
--     (00353). taskiem_customer_link_due (00468) and
--     taskiem_customer_waiting (00456) gain the guard.
--   * taskiem_customer_link_stale finds joined pairs whose person's phone
--     was verified after the pair was last looked at (the app thread's
--     link_checked_at, else when they were joined), or who have no
--     verified phone now: a phone change that did not reach the business.
--     The sweep looks at each and parts those whose WhatsApp thread is no
--     longer of the person's phone. Ids only.
-- +goose Up

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_link_due(p_limit int)
RETURNS TABLE (tenant_id uuid, conversation_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF cardinality(taskiem_tenant_scope()) <> 0 OR taskiem_person_scope() IS NOT NULL THEN
    RAISE EXCEPTION 'the sweep''s own, outside any business''s or person''s transaction' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT c.tenant_id, c.id FROM customer_conversations c JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active'
   WHERE c.line = 'app' AND c.link_checked_at IS NULL AND c.person_id IS NOT NULL
   ORDER BY c.id
   LIMIT LEAST(GREATEST(p_limit, 1), 500);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_waiting(p_after interval)
RETURNS TABLE (tenant_id uuid, conversation_id uuid, waiting_since timestamptz)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF cardinality(taskiem_tenant_scope()) <> 0 OR taskiem_person_scope() IS NOT NULL THEN
    RAISE EXCEPTION 'the sweep''s own, outside any business''s or person''s transaction' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT c.tenant_id, c.id, c.waiting_since FROM customer_conversations c JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active'
   WHERE c.line = 'app' AND c.waiting_since IS NOT NULL AND c.waiting_since < now() - p_after
     AND c.waiting_since > now() - interval '7 days'
     AND (c.owner_told_at IS NULL OR c.owner_told_at < c.waiting_since)
     AND (c.wait_check_at IS NULL OR c.wait_check_at <= now())
     AND c.blocked_at IS NULL AND c.person_blocked_at IS NULL
   ORDER BY c.waiting_since
   LIMIT 500;
END
$$;
-- +goose StatementEnd

-- The stale-link look reads which threads are joined, and since when.
GRANT SELECT (linked_to, linked_at) ON customer_conversations TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_customer_link_stale(p_limit int)
RETURNS TABLE (tenant_id uuid, conversation_id uuid)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF cardinality(taskiem_tenant_scope()) <> 0 OR taskiem_person_scope() IS NOT NULL THEN
    RAISE EXCEPTION 'the sweep''s own, outside any business''s or person''s transaction' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY SELECT a.tenant_id, a.id FROM customer_conversations w
    JOIN customer_conversations a ON a.tenant_id = w.tenant_id AND a.id = w.linked_to
    JOIN tenants t ON t.id = a.tenant_id AND t.status = 'active'
    LEFT JOIN users u ON u.id = a.person_id
    LEFT JOIN user_phones p ON p.user_id = a.person_id
   WHERE w.linked_to IS NOT NULL AND a.person_id IS NOT NULL
     AND (p.user_id IS NULL OR u.status IS DISTINCT FROM 'active' OR p.verified_at > COALESCE(a.link_checked_at, w.linked_at))
   ORDER BY a.id
   LIMIT LEAST(GREATEST(p_limit, 1), 500);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_customer_link_stale(integer)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_customer_link_stale(integer);
REVOKE SELECT (linked_to, linked_at) ON customer_conversations FROM taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_waiting(p_after interval)
RETURNS TABLE (tenant_id uuid, conversation_id uuid, waiting_since timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.id, c.waiting_since FROM customer_conversations c JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active'
   WHERE c.line = 'app' AND c.waiting_since IS NOT NULL AND c.waiting_since < now() - p_after
     AND c.waiting_since > now() - interval '7 days'
     AND (c.owner_told_at IS NULL OR c.owner_told_at < c.waiting_since)
     AND (c.wait_check_at IS NULL OR c.wait_check_at <= now())
     AND c.blocked_at IS NULL AND c.person_blocked_at IS NULL
   ORDER BY c.waiting_since
   LIMIT 500
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_customer_link_due(p_limit int)
RETURNS TABLE (tenant_id uuid, conversation_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.id FROM customer_conversations c JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active'
   WHERE c.line = 'app' AND c.link_checked_at IS NULL AND c.person_id IS NOT NULL
   ORDER BY c.id
   LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd
