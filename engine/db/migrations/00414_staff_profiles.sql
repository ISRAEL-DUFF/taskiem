-- A profile for each person of a business (docs/spec/chat-five-more-flows.md,
-- phase 3 plan, step 2): their job title, the day they started, a note,
-- their pay for the record (Taskiem pays no one here), and their usual
-- hours, which clocking in is measured against (step 3). One row per
-- person per business; none until someone fills it in.

-- +goose Up
CREATE TABLE staff_profiles (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  title       text NOT NULL DEFAULT '' CHECK (length(title) <= 60),
  started_on  date,
  note        text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  pay_kobo    bigint CHECK (pay_kobo IS NULL OR pay_kobo BETWEEN 0 AND 100000000000),
  pay_per     text CHECK (pay_per IN ('hour', 'day', 'week', 'month')),
  -- [{"day": "mon", "start": "08:00", "end": "16:00"}], an end before its
  -- start running past midnight.
  rota        jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(rota) = 'array'),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  text NOT NULL DEFAULT '',
  PRIMARY KEY (tenant_id, user_id),
  CHECK ((pay_kobo IS NULL) = (pay_per IS NULL))
);
ALTER TABLE staff_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE staff_profiles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_profiles TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON staff_profiles TO taskiem_app;

-- +goose Down
DROP TABLE staff_profiles;
