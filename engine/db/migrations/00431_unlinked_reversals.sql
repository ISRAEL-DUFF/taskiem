-- Money that went back and was not tied to its payment is looked at again
-- (docs/matching.md#money-that-went-back). A refund heard as pending and
-- then processed was recorded as taking back nothing (money_reversals with
-- entry_id NULL): the processed entry names the pending one in corrects,
-- which is not the payment, and a reversal once recorded was never looked
-- at again, so the invoice stayed paid. Matching now finds the payment by
-- its reference whatever the refund's earlier status, and a reversal
-- without its payment is tried again: every 15 minutes for 7 days, then
-- closed and a person told (closed_at). The rows the old code left are
-- tried once more the same way.
--
-- entry_id is set once, from NULL, when the payment is found; nothing else
-- about a reversal changes but how many matches it undid, when it was last
-- tried and when it was given up.
-- +goose Up
ALTER TABLE money_reversals ADD COLUMN checked_at timestamptz;
ALTER TABLE money_reversals ADD COLUMN closed_at timestamptz;
CREATE INDEX money_reversals_unlinked ON money_reversals (tenant_id, checked_at) WHERE entry_id IS NULL AND closed_at IS NULL;
GRANT UPDATE (entry_id, checked_at, closed_at) ON money_reversals TO taskiem_app;
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_reversal_kept() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF OLD.entry_id IS NOT NULL AND NEW.entry_id IS DISTINCT FROM OLD.entry_id THEN
    RAISE EXCEPTION 'a reversal names its payment once' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER money_reversal_kept BEFORE UPDATE ON money_reversals
  FOR EACH ROW EXECUTE FUNCTION taskiem_money_reversal_kept();

-- Tenants with money that went back to look at: not looked at yet, or
-- looked at and not tied to a payment, due another try.
GRANT SELECT (entry_id, checked_at, closed_at) ON money_reversals TO taskiem_dispatch;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_unreversed_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT e.tenant_id FROM money_entries e
   WHERE (e.direction = 'in' AND e.status = 'reversed' OR e.kind = 'refund' AND e.status = 'settled')
     AND NOT EXISTS (SELECT 1 FROM money_reversals r WHERE r.by_entry = e.id)
  UNION
  SELECT r.tenant_id FROM money_reversals r
   WHERE r.entry_id IS NULL AND r.closed_at IS NULL AND (r.checked_at IS NULL OR r.checked_at < now() - interval '15 minutes')
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_unreversed_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT e.tenant_id FROM money_entries e
   WHERE (e.direction = 'in' AND e.status = 'reversed' OR e.kind = 'refund' AND e.status = 'settled')
     AND NOT EXISTS (SELECT 1 FROM money_reversals r WHERE r.by_entry = e.id)
$$;
-- +goose StatementEnd
REVOKE SELECT (entry_id, checked_at, closed_at) ON money_reversals FROM taskiem_dispatch;
DROP TRIGGER money_reversal_kept ON money_reversals;
DROP FUNCTION taskiem_money_reversal_kept();
REVOKE UPDATE (entry_id, checked_at, closed_at) ON money_reversals FROM taskiem_app;
DROP INDEX money_reversals_unlinked;
ALTER TABLE money_reversals DROP COLUMN closed_at;
ALTER TABLE money_reversals DROP COLUMN checked_at;
