-- The sweep to the bank (docs/spec/iswallet-account-numbers.md N2): a
-- business receiving money with Taskiem may have what its wallet holds
-- sent to its saved bank account weekly, or whenever the wallet holds more
-- than an amount (checked hourly), keeping an amount it chooses. No
-- approval: the money only ever goes to the account written into the
-- sweep's workflow, which changes with the withdrawal one, under the same
-- governance. The workflow is Taskiem's (receive_money_sweep.json), made
-- and published by Settings > Payments.

-- +goose Up
ALTER TABLE receive_money
  ADD COLUMN sweep_mode text NOT NULL DEFAULT 'off' CHECK (sweep_mode IN ('off', 'weekly', 'above')),
  ADD COLUMN sweep_day text CHECK (sweep_day IN ('monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday')),
  ADD COLUMN sweep_time text CHECK (sweep_time ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  ADD COLUMN sweep_above_kobo bigint NOT NULL DEFAULT 0 CHECK (sweep_above_kobo >= 0),
  ADD COLUMN sweep_keep_kobo bigint NOT NULL DEFAULT 0 CHECK (sweep_keep_kobo >= 0),
  ADD COLUMN sweep_workflow_id uuid REFERENCES workflows(id),
  ADD CONSTRAINT receive_money_sweep_weekly CHECK (sweep_mode <> 'weekly' OR (sweep_day IS NOT NULL AND sweep_time IS NOT NULL)),
  ADD CONSTRAINT receive_money_sweep_above CHECK (sweep_mode <> 'above' OR sweep_above_kobo > 0);

-- +goose Down
ALTER TABLE receive_money DROP CONSTRAINT receive_money_sweep_above, DROP CONSTRAINT receive_money_sweep_weekly,
  DROP COLUMN sweep_workflow_id, DROP COLUMN sweep_keep_kobo, DROP COLUMN sweep_above_kobo,
  DROP COLUMN sweep_time, DROP COLUMN sweep_day, DROP COLUMN sweep_mode;
