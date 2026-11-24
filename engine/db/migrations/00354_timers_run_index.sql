-- +goose NO TRANSACTION
-- The retention purge (taskiem_purge_run, 00012) deletes a run's timers by
-- run_id, and deleting the runs row checks timers_run_id_fkey: with no
-- index on timers.run_id each purged run scanned every timer kept for
-- every retained run (fired timers stay until their run is purged), twice
-- (F35). timers is large, so the index is built CONCURRENTLY
-- (CONTRIBUTING.md, Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY timers_run ON timers (run_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS timers_run;
