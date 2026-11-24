-- Diary lines (docs/spec/chat-tools.md §3): a change made with a chat tool,
-- told as a line where it was made. In a conversation with a business it
-- is a row on this side only (no pair on the other side: the other
-- business never sees it); in the Taskiem conversation it is a line of the
-- person's own history, sealed like the rest.
-- +goose Up
ALTER TABLE link_messages DROP CONSTRAINT link_messages_kind_check;
ALTER TABLE link_messages ADD CONSTRAINT link_messages_kind_check CHECK (kind IN ('text', 'card', 'diary'));
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary'));
-- +goose Down
DELETE FROM link_messages WHERE kind = 'diary';
ALTER TABLE link_messages DROP CONSTRAINT link_messages_kind_check;
ALTER TABLE link_messages ADD CONSTRAINT link_messages_kind_check CHECK (kind IN ('text', 'card'));
DELETE FROM chat_history WHERE kind = 'diary';
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location'));
