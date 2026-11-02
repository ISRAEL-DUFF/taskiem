-- Business links, after the L6 review (decision 0061; docs/spec/business-links.md
-- §16.8). Four small things:
-- - taskiem_money_leak_tenants() also lists businesses with an open bill, so
--   a business that only buys through links (no money entries, no sent
--   invoices) gets the morning summary with its bills due (§10). The
--   dispatch role already reads bills' tenant_id, amount_kobo, paid_kobo and
--   voided (00292). Its owner and grants stay as 00204 set them.
-- - link_starts, the link trigger's queued starts (00298), keeps each for
--   90 days: taskiem_link_starts_purge() deletes older ones across
--   businesses, reading only when each was made, as the chat history's purge
--   does (00295). Its "open" index, which no query used (a start is found by
--   its id), is dropped.
-- - link@1's idempotency lookups (a run's reply by link_messages.sender_id,
--   a run's payment by bill_payments.created_by) get the indexes they scan.
-- Note on 00298's Down: it deletes the link triggers, but a deployed
-- workflow version whose definition starts on a "link" trigger stays
-- deployed with no trigger behind it; after a downgrade such a version fails
-- validation when published again until its trigger is changed. 00298 is not
-- edited (a migration already applied is never changed).
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_leak_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM money_entries UNION SELECT tenant_id FROM customer_invoices WHERE sent_at IS NOT NULL
  UNION SELECT tenant_id FROM stock_movements
   WHERE status = 'done' AND (short > 0 OR count_after < 0 OR (reason = 'count' AND change < 0))
  UNION SELECT tenant_id FROM bills WHERE NOT voided AND amount_kobo > paid_kobo
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_money_leak_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_money_leak_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_money_leak_tenants() TO taskiem_app;

-- Retention of queued starts: the dispatch role sees only when a start was
-- made, and deletes those past 90 days.
DROP INDEX link_starts_open;
CREATE POLICY dispatch_purge ON link_starts FOR DELETE TO taskiem_dispatch USING (created_at <= now() - interval '90 days');
CREATE POLICY dispatch_read_age ON link_starts FOR SELECT TO taskiem_dispatch USING (created_at <= now() - interval '90 days');
GRANT SELECT (id, created_at), DELETE ON link_starts TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_link_starts_purge()
RETURNS bigint LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  DELETE FROM link_starts s WHERE s.created_at <= now() - interval '90 days';
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_link_starts_purge() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_link_starts_purge() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_link_starts_purge() TO taskiem_app;

-- link@1's idempotency lookups.
CREATE INDEX link_messages_sender ON link_messages (tenant_id, sender_id);
CREATE INDEX bill_payments_by ON bill_payments (tenant_id, created_by, created_at DESC);

-- +goose Down
DROP INDEX bill_payments_by;
DROP INDEX link_messages_sender;
DROP FUNCTION taskiem_link_starts_purge();
REVOKE SELECT (id, created_at), DELETE ON link_starts FROM taskiem_dispatch;
DROP POLICY dispatch_read_age ON link_starts;
DROP POLICY dispatch_purge ON link_starts;
CREATE INDEX link_starts_open ON link_starts (tenant_id) WHERE done_at IS NULL;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_leak_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM money_entries UNION SELECT tenant_id FROM customer_invoices WHERE sent_at IS NOT NULL
  UNION SELECT tenant_id FROM stock_movements
   WHERE status = 'done' AND (short > 0 OR count_after < 0 OR (reason = 'count' AND change < 0))
$$;
-- +goose StatementEnd
