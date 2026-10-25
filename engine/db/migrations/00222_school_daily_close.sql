-- The daily close (decision 0046; docs/spec/schools.md 13.2, B9;
-- docs/schools.md#the-daily-close): one report per school and Lagos
-- day, taken exactly once. The row is inserted before the report is
-- computed (the key is taken first, as money_digests' is), in the same
-- transaction that stores the report, so a second scheduler, a restart or
-- a person asking for the close at the same moment find the row and read
-- it rather than computing another. A close is never recomputed: what the
-- books said when the day was closed is what the proprietor was sent.
-- +goose Up
CREATE TABLE school_daily_closes (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  day         date NOT NULL,                 -- the Lagos day closed
  reconciles  boolean NOT NULL,              -- nothing in the report fails to reconcile
  problems    int NOT NULL DEFAULT 0 CHECK (problems >= 0),
  report      jsonb NOT NULL,                -- the close (engine/schools.Close)
  ran_by      text NOT NULL,                 -- platform, or who asked for it
  ran_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, day)
);
CREATE INDEX school_daily_closes_list ON school_daily_closes (tenant_id, day DESC);
ALTER TABLE school_daily_closes ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_daily_closes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_daily_closes TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_daily_closes TO taskiem_app;
GRANT UPDATE (reconciles, problems, report, ran_at) ON school_daily_closes TO taskiem_app;

-- The key is taken as an empty row, filled in once with the report in the
-- same transaction; after that a close is never changed or deleted.
-- +goose StatementBegin
CREATE FUNCTION taskiem_school_close_kept() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND OLD.report = '{}'::jsonb AND NEW.report <> '{}'::jsonb THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'a daily close is never changed' USING ERRCODE = '23514';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER school_close_kept BEFORE UPDATE OR DELETE ON school_daily_closes
  FOR EACH ROW EXECUTE FUNCTION taskiem_school_close_kept();

-- +goose Down
DROP TRIGGER school_close_kept ON school_daily_closes;
DROP FUNCTION taskiem_school_close_kept();
DROP TABLE school_daily_closes;
