-- Business links, L3: the plan's messages per day (docs/spec/business-links.md
-- §13, "messages per day are plan limits, counted as other limits are").
-- A message to a linked business (text, a photo, a file or a voice note;
-- not the cards Taskiem writes) counts against the business that sent
-- it, per UTC day; beyond the limit a send is refused as every other
-- limit is (429, limit_exceeded). 0 or unset: no limit. Set with
-- taskiem_set_tenant_limits like any limit, from the plan (deploy/plans.yaml).
-- +goose Up
ALTER TABLE tenant_limits ADD COLUMN link_messages_daily bigint CHECK (link_messages_daily >= 0);
-- The day's count, per business.
CREATE INDEX link_messages_sent ON link_messages (tenant_id, created_at) WHERE mine AND kind = 'text';

-- +goose Down
DROP INDEX link_messages_sent;
ALTER TABLE tenant_limits DROP COLUMN link_messages_daily;
