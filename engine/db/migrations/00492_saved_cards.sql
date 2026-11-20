-- A saved thing shared in a people chat or a group (saved objects S1, part
-- 3; docs/spec/saved-objects.md §6.2): a card of type saved, sealed with the
-- message as every card is.
-- +goose Up
ALTER TABLE people_messages DROP CONSTRAINT people_messages_card_type_check,
  ADD CONSTRAINT people_messages_card_type_check CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split', 'basket', 'saved'));
ALTER TABLE group_messages DROP CONSTRAINT group_messages_card_type_check,
  ADD CONSTRAINT group_messages_card_type_check CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split', 'basket', 'saved'));

-- +goose Down
DELETE FROM people_messages WHERE card_type = 'saved';
DELETE FROM group_messages WHERE card_type = 'saved';
ALTER TABLE people_messages DROP CONSTRAINT people_messages_card_type_check,
  ADD CONSTRAINT people_messages_card_type_check CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split', 'basket'));
ALTER TABLE group_messages DROP CONSTRAINT group_messages_card_type_check,
  ADD CONSTRAINT group_messages_card_type_check CHECK (card_type IN ('product', 'business', 'order', 'delivery', 'split', 'basket'));
