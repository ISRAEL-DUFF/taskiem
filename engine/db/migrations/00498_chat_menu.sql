-- Menus in the chat (docs/spec/chat-menus.md, M1): a menu in the Taskiem
-- conversation is kept as a message of kind menu (its text the menu's
-- topic, line and picked row, as JSON).
-- +goose Up
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary', 'chart', 'menu'));

-- +goose Down
DELETE FROM chat_history WHERE kind = 'menu';
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary', 'chart'));
