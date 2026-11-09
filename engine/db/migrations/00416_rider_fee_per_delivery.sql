-- Riders as participants, phase 4 (docs/spec/chat-riders.md, decision
-- 0065): riders are paid per delivery. A delivery's fee is due when the
-- rider taps Delivered and payable when the business confirms it: the
-- confirm makes a payout of that one delivery, paid through the payout
-- path that exists (pay-riders, 00243) or recorded as paid in cash by
-- hand. Cash the rider took at the door nets the fee as a batch nets it.
-- +goose Up
ALTER TABLE deliveries
  ADD COLUMN confirmed_at timestamptz,
  ADD COLUMN confirmed_by text;
ALTER TABLE rider_payouts
  ADD COLUMN delivery_id uuid REFERENCES deliveries(id),
  ADD COLUMN how text NOT NULL DEFAULT 'bank' CHECK (how IN ('bank', 'cash'));
CREATE INDEX rider_payouts_delivery ON rider_payouts (delivery_id) WHERE delivery_id IS NOT NULL;

-- +goose Down
DROP INDEX rider_payouts_delivery;
ALTER TABLE rider_payouts DROP COLUMN how, DROP COLUMN delivery_id;
ALTER TABLE deliveries DROP COLUMN confirmed_by, DROP COLUMN confirmed_at;
