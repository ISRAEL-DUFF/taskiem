-- Staff by phone (docs/spec/chat-five-more-flows.md, phase 3 plan, step 1).
-- An invitation by phone, from chat or now from the web, carries the
-- person's name, made theirs when they join; one may be cancelled before
-- it is answered; and when it was last sent, so it is not sent too often.

-- +goose Up
ALTER TABLE chat_invites ADD COLUMN name text NOT NULL DEFAULT '' CHECK (length(name) <= 80);
ALTER TABLE chat_invites ADD COLUMN sent_at timestamptz;
ALTER TABLE chat_invites DROP CONSTRAINT chat_invites_state_check;
ALTER TABLE chat_invites ADD CONSTRAINT chat_invites_state_check CHECK (state IN ('asked', 'sent', 'joined', 'declined', 'cancelled'));

-- +goose Down
UPDATE chat_invites SET state = 'declined' WHERE state = 'cancelled';
ALTER TABLE chat_invites DROP CONSTRAINT chat_invites_state_check;
ALTER TABLE chat_invites ADD CONSTRAINT chat_invites_state_check CHECK (state IN ('asked', 'sent', 'joined', 'declined'));
ALTER TABLE chat_invites DROP COLUMN sent_at;
ALTER TABLE chat_invites DROP COLUMN name;
