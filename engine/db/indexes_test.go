package db_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// leadingIndex reports whether a valid index of table leads with column
// and, when partial is empty, covers every row; otherwise its predicate
// must be exactly partial (as pg_get_expr prints it).
func leadingIndex(t *testing.T, d *dbtest.DB, table, column, partial string) bool {
	t.Helper()
	var ok bool
	if err := d.Admin.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[0]
		WHERE i.indrelid = $1::regclass AND a.attname = $2 AND i.indisvalid
		  AND COALESCE(pg_get_expr(i.indpred, i.indrelid), '') = $3)`, table, column, partial).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// The retention purge (taskiem_purge_run) deletes a run's timers by
// run_id, and deleting the run checks timers' foreign key: without an
// index on timers.run_id each purged run scanned every timer kept for
// every retained run, twice (F35).
func TestTimersAreIndexedByRun(t *testing.T) {
	d := dbtest.New(t)
	if !leadingIndex(t, d, "timers", "run_id", "") {
		t.Error("no index on timers (run_id): the retention purge scans every timer per run")
	}
}
