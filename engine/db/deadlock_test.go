package db_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// Two transactions that lock the same two rows in opposite orders: the
// server ends one of them (deadlock_detected). It rolled back, so the
// retry runs it again, and both land.
func TestRetryADeadlockVictim(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	for _, name := range []string{"a", "b"} {
		if err := db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error { return setVar(ctx, tx, tn.ID, name) }); err != nil {
			t.Fatal(err)
		}
	}
	bump := func(tx pgx.Tx, name string) error {
		_, err := tx.Exec(ctx, `UPDATE variables SET value = ((value #>> '{}')::int + 1)::text::jsonb WHERE tenant_id = $1 AND name = $2`, tn.ID, name)
		return err
	}
	var attempts atomic.Int32
	var both sync.WaitGroup // each holds its first row before either asks for its second
	both.Add(2)
	run := func(first, second string) error {
		tries := 0
		return db.InTenantTxRetry(ctx, d.App, []uuid.UUID{tn.ID}, true, func(tx pgx.Tx) error {
			attempts.Add(1)
			tries++
			if err := bump(tx, first); err != nil {
				return err
			}
			if tries == 1 {
				both.Done()
				both.Wait()
			}
			return bump(tx, second)
		})
	}
	errs := make(chan error, 2)
	go func() { errs <- run("a", "b") }()
	go func() { errs <- run("b", "a") }()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Errorf("a deadlock's victim was not run again: %v", err)
		}
	}
	if attempts.Load() != 3 {
		t.Errorf("%d attempts, want 3: one of the two ran twice", attempts.Load())
	}
	for _, name := range []string{"a", "b"} {
		var v string
		if err := d.Admin.QueryRow(ctx, `SELECT value #>> '{}' FROM variables WHERE tenant_id = $1 AND name = $2`, tn.ID, name).Scan(&v); err != nil || v != "3" {
			t.Errorf("variable %s is %s, want 3 (each transaction landed once): %v", name, v, err)
		}
	}
}
