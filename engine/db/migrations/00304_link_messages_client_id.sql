-- Business links, L3: a message sent from the web carries an id its sender
-- made (client_id), so a send repeated after a dropped connection (the
-- chat's Retry) is the same message, never a second one: the second send
-- finds the first and answers it. Unique per business and conversation, on
-- the sender's own copy only; messages without one (chat, the API, a
-- forward, cards) are as they were.
-- +goose Up
ALTER TABLE link_messages ADD COLUMN client_id uuid;
CREATE UNIQUE INDEX link_messages_client ON link_messages (tenant_id, link_id, client_id) WHERE client_id IS NOT NULL;

-- +goose Down
DROP INDEX link_messages_client;
ALTER TABLE link_messages DROP COLUMN client_id;
