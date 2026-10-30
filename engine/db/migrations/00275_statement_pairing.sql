-- Pairing a statement line with a transfer entered by hand (decision 0060;
-- docs/spec/no-sheet-needed.md §6.4, R5e). "Ada paid 2,000 transfer" into
-- an account Taskiem does not read is entered at once under that
-- account's statement channel (gtbank/shop), so her balance moves now.
-- The bank statement uploaded later carries the same money. Without
-- pairing, the import recorded it as a second payment.
--
-- On import, a money-in line on the channel an entered transfer named,
-- of the same amount and within three days of it, is recorded as the
-- bank's confirmation of that entry (corrects it: one movement, counted
-- once, and never matched again). Exactly one such transfer pairs at once.
-- Several, or one alone with a different amount, wait here, not yet in
-- the log, for a person to say Same money or Different money. A line
-- waiting is not imported twice: its key is kept here too.
-- +goose Up
CREATE TABLE money_statement_waiting (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  channel      text NOT NULL,
  entry_key    text NOT NULL,
  line         jsonb NOT NULL,           -- occurred_at, amount_kobo, reference, description
  candidates   uuid[] NOT NULL CHECK (cardinality(candidates) BETWEEN 1 AND 20),
  uploaded_by  text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  decision     text CHECK (decision IN ('same', 'different')),
  entry_id     uuid REFERENCES money_entries(id),   -- the line as recorded once decided
  decided_by   text,
  decided_at   timestamptz,
  UNIQUE (tenant_id, entry_key),
  CHECK ((decision IS NULL) = (decided_at IS NULL))
);
CREATE INDEX money_statement_waiting_open ON money_statement_waiting (tenant_id, created_at) WHERE decision IS NULL;
ALTER TABLE money_statement_waiting ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_statement_waiting FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_statement_waiting TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON money_statement_waiting TO taskiem_app;
GRANT UPDATE (decision, entry_id, decided_by, decided_at) ON money_statement_waiting TO taskiem_app;

-- +goose Down
DROP TABLE money_statement_waiting;
