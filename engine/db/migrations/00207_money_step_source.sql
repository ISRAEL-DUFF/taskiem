-- The money log also records a payout a rail reports as settled when it
-- is made (a pay_bank step answering settled), with no event after
-- (docs/money-log.md): its source is 'step'. It is keyed as the rail's
-- payout.settled event would be, so an event that does come later is the
-- same entry.
-- +goose Up
ALTER TABLE money_entries DROP CONSTRAINT money_entries_source_check;
ALTER TABLE money_entries ADD CONSTRAINT money_entries_source_check CHECK (source IN ('event', 'lookup', 'step', 'upload', 'entered'));

-- +goose Down
ALTER TABLE money_entries DROP CONSTRAINT money_entries_source_check;
ALTER TABLE money_entries ADD CONSTRAINT money_entries_source_check CHECK (source IN ('event', 'lookup', 'upload', 'entered'));
