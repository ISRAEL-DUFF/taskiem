-- A trigger receipt dedups a delivery for 30 days (engine findings, Atlas
-- part C): long past any provider's retries, after which an identical
-- delivery is new again and the receipt is purged. Before this the table
-- was never pruned, and a body sent again months later was dropped.

-- +goose Up
CREATE INDEX trigger_receipts_received_at ON trigger_receipts (received_at);

-- A receipt older than the window is taken over by the new delivery
-- (runtime.receiptWindow): INSERT ... ON CONFLICT DO UPDATE.
GRANT UPDATE (run_id, received_at) ON trigger_receipts TO taskiem_app;

-- Deletes receipts past the window, the oldest day of them at a time, for
-- every tenant. The window is fixed here, not passed in, so a caller
-- cannot clear fresh receipts and have a delivery replayed.
-- +goose StatementBegin
CREATE FUNCTION taskiem_purge_trigger_receipts()
RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_cutoff timestamptz := now() - interval '30 days';
  v_oldest timestamptz;
  v_n bigint;
BEGIN
  SELECT min(received_at) INTO v_oldest FROM trigger_receipts;
  IF v_oldest IS NULL OR v_oldest >= v_cutoff THEN
    RETURN 0;
  END IF;
  DELETE FROM trigger_receipts WHERE received_at < least(v_cutoff, v_oldest + interval '1 day');
  GET DIAGNOSTICS v_n = ROW_COUNT;
  RETURN v_n;
END
$$;
-- +goose StatementEnd
CREATE POLICY dispatch_purge ON trigger_receipts FOR DELETE TO taskiem_dispatch USING (true);
CREATE POLICY dispatch_select ON trigger_receipts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (received_at), DELETE ON trigger_receipts TO taskiem_dispatch;
ALTER FUNCTION taskiem_purge_trigger_receipts() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_purge_trigger_receipts() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_purge_trigger_receipts() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_purge_trigger_receipts();
REVOKE SELECT (received_at), DELETE ON trigger_receipts FROM taskiem_dispatch;
DROP POLICY dispatch_select ON trigger_receipts;
DROP POLICY dispatch_purge ON trigger_receipts;
REVOKE UPDATE (run_id, received_at) ON trigger_receipts FROM taskiem_app;
DROP INDEX trigger_receipts_received_at;
