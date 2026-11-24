-- A reaction may be any one emoji, as WhatsApp's "+" gives (before, only the
-- six it offers first). The words of the check move to the API, which reads
-- a reaction as one emoji (engine/links.ValidReaction); the table keeps only
-- a bound on its size.
-- +goose Up
ALTER TABLE link_message_reactions DROP CONSTRAINT IF EXISTS link_message_reactions_emoji_check;
ALTER TABLE link_message_reactions ADD CONSTRAINT link_message_reactions_emoji_size CHECK (char_length(emoji) BETWEEN 1 AND 16);

-- +goose Down
DELETE FROM link_message_reactions WHERE emoji NOT IN ('👍', '❤️', '😂', '😮', '😢', '🙏');
ALTER TABLE link_message_reactions DROP CONSTRAINT IF EXISTS link_message_reactions_emoji_size;
ALTER TABLE link_message_reactions ADD CONSTRAINT link_message_reactions_emoji_check CHECK (emoji IN ('👍', '❤️', '😂', '😮', '😢', '🙏'));
