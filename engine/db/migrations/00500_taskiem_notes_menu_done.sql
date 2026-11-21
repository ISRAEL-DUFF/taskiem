-- "@Taskiem" in a conversation with a linked business (chat-tools.md): the
-- message goes to Taskiem, never the business, and what Taskiem answered is
-- kept in that conversation as a note on this side only (kind taskiem):
-- the words asked, and the answer as its body. Its client id makes a
-- repeated send answer the same note rather than ask Taskiem again.
--
-- Menus in the Taskiem chat (chat-menus.md): how a menu ended, so it folds
-- to a line saying so: chose:<action id>, closed (its ✕), or passed (the
-- person said something else, or a newer menu came).
-- +goose Up
ALTER TABLE link_messages DROP CONSTRAINT link_messages_kind_check;
ALTER TABLE link_messages ADD CONSTRAINT link_messages_kind_check CHECK (kind IN ('text', 'card', 'diary', 'taskiem'));
ALTER TABLE link_messages ADD COLUMN asked text CHECK (asked IS NULL OR length(asked) <= 4000);
ALTER TABLE chat_history ADD COLUMN menu_done text CHECK (menu_done IS NULL OR menu_done ~ '^(closed|passed|chose:[a-z]+\.[a-z_]+)$');
-- Only menu_done is ever changed after a message is kept.
GRANT UPDATE (menu_done) ON chat_history TO taskiem_app;

-- +goose Down
REVOKE UPDATE (menu_done) ON chat_history FROM taskiem_app;
ALTER TABLE chat_history DROP COLUMN menu_done;
DELETE FROM link_messages WHERE kind = 'taskiem';
ALTER TABLE link_messages DROP COLUMN asked;
ALTER TABLE link_messages DROP CONSTRAINT link_messages_kind_check;
ALTER TABLE link_messages ADD CONSTRAINT link_messages_kind_check CHECK (kind IN ('text', 'card', 'diary'));
