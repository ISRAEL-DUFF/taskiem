-- Chat tools phase 5 (docs/spec/chat-tools.md §7): "how did we do?" is
-- answered in the Taskiem conversation with a chart card, kept as a
-- message of kind chart (its text the chart's numbers, as JSON).
-- +goose Up
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary', 'chart'));

-- +goose Down
DELETE FROM chat_history WHERE kind = 'chart';
ALTER TABLE chat_history DROP CONSTRAINT chat_history_kind_check;
ALTER TABLE chat_history ADD CONSTRAINT chat_history_kind_check CHECK (kind IN ('text', 'button', 'voice', 'photo', 'form', 'location', 'diary'));
