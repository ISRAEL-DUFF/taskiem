-- Subscriptions a connection holds (connector/v1 registration: connection;
-- docs/spec/iswallet-account-numbers.md I7). Some providers have no page
-- where a business pastes Taskiem's webhook URL: the subscription is made
-- through their API and its signing secret is returned once (iswallet).
-- Taskiem then holds one subscription per connection, for every event its
-- trigger sends, made when the connection is and removed with it, whichever
-- workflows use the trigger. Such a row has no workflow; the reconciler
-- treats it as any other (decision 0021), and ingest delivers its events to
-- every subscribed workflow and the money log, as a pasted webhook's.

-- +goose Up
ALTER TABLE remote_subscriptions ALTER COLUMN workflow_id DROP NOT NULL;
-- One live subscription per connection, trigger and environment.
CREATE UNIQUE INDEX remote_subscriptions_connection_live ON remote_subscriptions (tenant_id, environment, connector, trigger_name, COALESCE(connection, ''))
  WHERE workflow_id IS NULL AND desired = 'present';

-- +goose Down
DROP INDEX remote_subscriptions_connection_live;
DELETE FROM remote_subscriptions WHERE workflow_id IS NULL;
ALTER TABLE remote_subscriptions ALTER COLUMN workflow_id SET NOT NULL;
