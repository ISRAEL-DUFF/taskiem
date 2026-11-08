-- Polling a busy provider without skipping or rereading records
-- (connector/v1 rule 22; docs/contracts/connector-v1.md).
--
-- A polled trigger whose action reports the keys it read (poll.keys)
-- keeps the latest of them here, at most 2,000, and is given them back
-- (poll.seen), so the overlap each poll looks back over is not read
-- again (Gmail's new_mail read every message in it in full). The column
-- is the tenant's, under trigger_polls' row-level security; the app
-- role's table-wide grant (00189) covers it.
-- +goose Up
ALTER TABLE trigger_polls ADD COLUMN seen text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE trigger_polls DROP COLUMN seen;
