-- What a standing discount was worth when it was given (docs/spec/
-- schools.md 8; docs/schools.md#the-second-person): the school's
-- discount threshold weighs a further discount or reduction for a
-- student with the discounts given them without a second person within
-- the window, and a percent's worth depends on the fees of the term it
-- was weighed in. Null for discounts given before this; their amount (a
-- percent: nothing) is weighed instead.
-- +goose Up
ALTER TABLE school_discounts ADD COLUMN worth_kobo bigint CHECK (worth_kobo >= 0);

-- +goose Down
ALTER TABLE school_discounts DROP COLUMN worth_kobo;
