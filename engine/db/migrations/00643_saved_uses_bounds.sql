-- The record of runs reading saved things (00641) is bounded (H6 review):
-- a row goes with its run when the run is purged (retention, erasure,
-- whichever path deletes it), and the application keeps at most the latest
-- few hundred for each saved thing (api/saved_runs.go, savedUsesKept).
-- The trigger runs as taskiem_dispatch, which may delete a run's rows and
-- read nothing but their business and run (by the tenant_id-led index).
-- +goose Up
CREATE POLICY dispatch_forget ON saved_uses TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, run_id), DELETE ON saved_uses TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_saved_uses_run_gone() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  DELETE FROM saved_uses WHERE tenant_id = OLD.tenant_id AND run_id = OLD.id;
  RETURN OLD;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_saved_uses_run_gone() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_saved_uses_run_gone() FROM PUBLIC;
CREATE TRIGGER saved_uses_run_gone AFTER DELETE ON runs
  FOR EACH ROW EXECUTE FUNCTION taskiem_saved_uses_run_gone();

-- +goose Down
DROP TRIGGER saved_uses_run_gone ON runs;
DROP FUNCTION taskiem_saved_uses_run_gone();
REVOKE SELECT (tenant_id, run_id), DELETE ON saved_uses FROM taskiem_dispatch;
DROP POLICY dispatch_forget ON saved_uses;
