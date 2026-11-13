-- Money recorded for an earlier day is in exactly one daily close
-- (docs/schools.md#the-daily-close). A close counted money in by the day
-- it was paid on and is taken once, so cash posted on the 13th with the
-- date of the 10th, or a transfer the bank told of after the night's
-- close had run, was in no close at all: not the frozen close of its own
-- day, nor the close of the day it was recorded on. A close now also
-- carries what was recorded in its day for earlier days, and keeps here
-- which entries it counted in, so no entry is in two closes and none the
-- closes could count is in none. Rows are written with the close and
-- never changed (the app role may only insert and read).
-- +goose Up
CREATE TABLE school_close_entries (
  entry_id   uuid PRIMARY KEY REFERENCES money_entries(id),
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  close_id   uuid NOT NULL REFERENCES school_daily_closes(id)
);
CREATE INDEX school_close_entries_close ON school_close_entries (tenant_id, close_id);
ALTER TABLE school_close_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_close_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_close_entries TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_close_entries TO taskiem_app;

-- +goose Down
DROP TABLE school_close_entries;
