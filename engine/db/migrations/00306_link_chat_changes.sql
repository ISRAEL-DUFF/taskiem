-- Business links, L3: what changed in the conversations, for the Chat page's
-- live stream (GET /v1/chat/stream): each connection asks every second what
-- changed since it last looked, from an index, with no new mechanism between
-- the writers and the readers (the performance document warns against
-- NOTIFY on the database that carries the runs).
--
-- A message row says when it last changed (updated_at): when it was written,
-- read, hidden, or its reactions changed. The columns already there carry
-- the rest: a conversation's settings (link_conversations.updated_at).
-- updated_at is the statement's clock, so a change that commits a little
-- later than it was written is read again within the stream's look-back.
-- +goose Up
ALTER TABLE link_messages ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE link_messages ALTER COLUMN updated_at SET DEFAULT clock_timestamp();
CREATE INDEX link_messages_changed ON link_messages (tenant_id, updated_at);
CREATE INDEX link_conversations_changed ON link_conversations (tenant_id, updated_at);

-- +goose StatementBegin
CREATE FUNCTION link_messages_touch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := clock_timestamp();
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER link_messages_touch BEFORE UPDATE ON link_messages
  FOR EACH ROW EXECUTE FUNCTION link_messages_touch();

-- A reaction given, changed or taken back changes its message. The trigger
-- runs as the writer, in the scope of the side whose copy it is.
-- +goose StatementBegin
CREATE FUNCTION link_message_reactions_touch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  r link_message_reactions;
BEGIN
  r := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
  UPDATE link_messages SET updated_at = clock_timestamp() WHERE tenant_id = r.tenant_id AND pair_id = r.pair_id;
  RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER link_message_reactions_touch AFTER INSERT OR UPDATE OR DELETE ON link_message_reactions
  FOR EACH ROW EXECUTE FUNCTION link_message_reactions_touch();

-- +goose Down
DROP TRIGGER link_message_reactions_touch ON link_message_reactions;
DROP FUNCTION link_message_reactions_touch();
DROP TRIGGER link_messages_touch ON link_messages;
DROP FUNCTION link_messages_touch();
DROP INDEX link_conversations_changed;
DROP INDEX link_messages_changed;
ALTER TABLE link_messages DROP COLUMN updated_at;
