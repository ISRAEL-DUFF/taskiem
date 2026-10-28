-- Money a person entered, matched by hand, waits for a second person as
-- cash at the desk does (docs/spec/schools.md 8; docs/schools.md#cash-and-transfers):
-- where the school has a cash threshold, matching an entered cash entry
-- or an uploaded statement line to an invoice at or above it records a
-- cash_match request in school_pending_postings instead of counting it.
-- Approved, the match is made then, once.
-- +goose Up
ALTER TABLE school_pending_postings DROP CONSTRAINT school_pending_postings_kind_check;
ALTER TABLE school_pending_postings ADD CONSTRAINT school_pending_postings_kind_check
  CHECK (kind IN ('cash_posting', 'cash_match', 'discount', 'discount_amendment', 'term_invoice'));

-- +goose Down
DELETE FROM school_pending_postings WHERE kind = 'cash_match';
ALTER TABLE school_pending_postings DROP CONSTRAINT school_pending_postings_kind_check;
ALTER TABLE school_pending_postings ADD CONSTRAINT school_pending_postings_kind_check
  CHECK (kind IN ('cash_posting', 'discount', 'discount_amendment', 'term_invoice'));
