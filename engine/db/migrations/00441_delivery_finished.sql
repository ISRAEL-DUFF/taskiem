-- A delivery could be delivered while its order never completed
-- (docs/dispatch.md). The rider's Delivered tap saved the delivery, and
-- completing the order, telling its workflow and telling the customer ran
-- after the commit: a process that died there, or a database error, left
-- the order "arrived" for ever, and a second tap did nothing. The order
-- is now completed in the tap's own transaction; finished_at says the
-- rest was carried through, and the dispatch sweep finishes delivered
-- deliveries where it was not. Deliveries delivered before this are taken
-- as finished.
-- +goose Up
ALTER TABLE deliveries ADD COLUMN finished_at timestamptz;
UPDATE deliveries SET finished_at = COALESCE(delivered_at, now()) WHERE status = 'delivered';
CREATE INDEX deliveries_unfinished ON deliveries (tenant_id, delivered_at) WHERE status = 'delivered' AND finished_at IS NULL;
GRANT SELECT (finished_at) ON deliveries TO taskiem_dispatch;

-- The sweep's businesses: also those with a delivery to finish.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_dispatch_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.tenant_id FROM deliveries d JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active'
   WHERE d.status IN ('offered', 'accepted', 'picked_up', 'arrived') OR d.rated_at > now() - interval '1 day'
      OR (d.status = 'delivered' AND d.finished_at IS NULL AND d.delivered_at > now() - interval '7 days')
  UNION
  SELECT r.tenant_id FROM rider_remittances r JOIN tenants t ON t.id = r.tenant_id AND t.status = 'active' WHERE r.settled_at IS NULL
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_dispatch_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT d.tenant_id FROM deliveries d JOIN tenants t ON t.id = d.tenant_id AND t.status = 'active'
   WHERE d.status IN ('offered', 'accepted', 'picked_up', 'arrived') OR d.rated_at > now() - interval '1 day'
  UNION
  SELECT r.tenant_id FROM rider_remittances r JOIN tenants t ON t.id = r.tenant_id AND t.status = 'active' WHERE r.settled_at IS NULL
$$;
-- +goose StatementEnd
DROP INDEX deliveries_unfinished;
ALTER TABLE deliveries DROP COLUMN finished_at;
