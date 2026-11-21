-- Paying a bill by transfer (the payments flow, part 1): what both chats
-- were last told of a bill's number (its seq and paid or part, so each is
-- said once), and whether the seller's provider gives a longer time than
-- the default ("I need longer" is offered only then).
-- +goose Up
ALTER TABLE bills ADD COLUMN transfer_told text,
  ADD COLUMN transfer_longer boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE bills DROP COLUMN transfer_longer, DROP COLUMN transfer_told;
