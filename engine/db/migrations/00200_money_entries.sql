-- The money log (decision 0045 point 6; docs/spec/invoices-and-money-log.md
-- 7, T2; docs/money-log.md): one row per movement of money on a business's
-- connected rails, and per later status of it, written once by Taskiem and
-- never changed. The app role may insert and read; the only column it may
-- update is mirrored_at, set when the row has been copied to the business's
-- kept data table money_log, where the owner reads, searches and exports it.
--
-- A movement is keyed by where it happened (the connection), what Taskiem
-- heard (the normalised event) and the rail's reference, so the same event
-- delivered twice, polled as well as pushed, or looked up as well, is one
-- row. A later status (a payout reversed after it settled) is its own row
-- that names the first in corrects.
-- +goose Up
CREATE TABLE money_entries (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  entry_key     text NOT NULL CHECK (length(entry_key) <= 400),
  occurred_at   timestamptz,
  recorded_at   timestamptz NOT NULL DEFAULT now(),
  direction     text NOT NULL CHECK (direction IN ('in', 'out')),
  kind          text NOT NULL CHECK (kind IN ('payment', 'account_credit', 'payout', 'refund', 'pos_sale', 'fee', 'statement', 'cash', 'expense', 'correction')),
  amount_kobo   bigint CHECK (amount_kobo >= 0),
  fee_kobo      bigint CHECK (fee_kobo >= 0),
  currency      text NOT NULL DEFAULT 'NGN' CHECK (currency ~ '^[A-Z]{3}$'),
  status        text NOT NULL CHECK (status IN ('settled', 'pending', 'failed', 'reversed')),
  channel       text NOT NULL CHECK (length(channel) <= 200),
  reference     text NOT NULL CHECK (length(reference) <= 200),
  event         text,                       -- the normalised event, when it came from one
  payer         jsonb,                      -- sealed: {name, account}
  source        text NOT NULL CHECK (source IN ('event', 'lookup', 'upload', 'entered')),
  note          text CHECK (length(note) <= 300),
  corrects      uuid REFERENCES money_entries(id),
  mirrored_at   timestamptz,
  UNIQUE (tenant_id, entry_key)
);
CREATE INDEX money_entries_list ON money_entries (tenant_id, recorded_at DESC);
CREATE INDEX money_entries_reference ON money_entries (tenant_id, channel, reference);
CREATE INDEX money_entries_unmirrored ON money_entries (recorded_at) WHERE mirrored_at IS NULL;
ALTER TABLE money_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_entries TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON money_entries TO taskiem_app;
GRANT UPDATE (mirrored_at) ON money_entries TO taskiem_app;

-- mirrored_at is set once; nothing else about a row ever changes.
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_entry_kept() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.mirrored_at IS NOT NULL OR NEW.mirrored_at IS NULL THEN
    RAISE EXCEPTION 'a money log entry is never changed' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER money_entry_kept BEFORE UPDATE ON money_entries
  FOR EACH ROW EXECUTE FUNCTION taskiem_money_entry_kept();

-- Tenants with entries still to copy to their kept table.
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_unmirrored_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT e.tenant_id FROM money_entries e WHERE e.mirrored_at IS NULL
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_money_unmirrored_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_money_unmirrored_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_money_unmirrored_tenants() TO taskiem_app;
CREATE POLICY dispatch ON money_entries FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT ON money_entries TO taskiem_dispatch;

-- The log's own intake (spec 7.3): every active prod connection, so the
-- poller asks a rail about its money events even when no workflow listens.
-- Names only, never credentials.
CREATE POLICY dispatch_money ON connections FOR SELECT TO taskiem_dispatch USING (environment = 'prod' AND status = 'active');
GRANT SELECT (tenant_id, environment, connector, name, status) ON connections TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_connections()
RETURNS TABLE (tenant_id uuid, connector text, connection text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.connector, c.name FROM connections c JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active'
   WHERE c.environment = 'prod' AND c.status = 'active'
   ORDER BY 1, 2, 3
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_money_connections() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_money_connections() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_money_connections() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_money_connections();
REVOKE SELECT (tenant_id, environment, connector, name, status) ON connections FROM taskiem_dispatch;
DROP POLICY dispatch_money ON connections;
DROP FUNCTION taskiem_money_unmirrored_tenants();
DROP TABLE money_entries;
DROP FUNCTION taskiem_money_entry_kept();
