package db_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// Functions added with AfterCommit run once, in order, after a commit, and
// never after a rollback; one that panics does not undo the commit or stop
// the others (docs/spec/task-wake-ups.md, 5.1).
func TestAfterCommitRunsOnlyOnCommit(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	scope := []uuid.UUID{tn.ID}

	var ran []int
	err := db.InTenantTx(ctx, d.App, scope, func(tx pgx.Tx) error {
		for i := 1; i <= 3; i++ {
			if !db.AfterCommit(tx, func() { ran = append(ran, i) }) {
				t.Fatal("a transaction from InTenantTx refused an after-commit function")
			}
		}
		if len(ran) != 0 {
			t.Errorf("functions ran before the commit: %v", ran)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 3 || ran[0] != 1 || ran[1] != 2 || ran[2] != 3 {
		t.Errorf("after a commit ran %v, want [1 2 3]", ran)
	}

	ran = nil
	failed := errors.New("no")
	err = db.InTenantTx(ctx, d.App, scope, func(tx pgx.Tx) error {
		db.AfterCommit(tx, func() { ran = append(ran, 1) })
		return failed
	})
	if !errors.Is(err, failed) || len(ran) != 0 {
		t.Errorf("after a rollback: err %v, ran %v, want the error and nothing run", err, ran)
	}

	// A statement that fails at commit time rolls back too.
	ran = nil
	err = db.InTenantTx(ctx, d.App, scope, func(tx pgx.Tx) error {
		db.AfterCommit(tx, func() { ran = append(ran, 1) })
		_, _ = tx.Exec(ctx, `SELECT 1/0`) // the transaction is now aborted
		return nil
	})
	if err == nil || len(ran) != 0 {
		t.Errorf("after a failed commit: err %v, ran %v, want an error and nothing run", err, ran)
	}

	ran = nil
	err = db.InTenantTx(ctx, d.App, scope, func(tx pgx.Tx) error {
		db.AfterCommit(tx, func() { panic("boom") })
		db.AfterCommit(tx, func() { ran = append(ran, 2) })
		return nil
	})
	if err != nil || len(ran) != 1 {
		t.Errorf("a panic after the commit: err %v, ran %v, want no error and the next function run", err, ran)
	}
}

// A savepoint and a transaction begun some other way cannot hold an
// after-commit function, so their callers keep what they did before.
func TestAfterCommitRefusedOnSavepoint(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if db.AfterCommit(sp, func() {}) {
			t.Error("a savepoint accepted an after-commit function")
		}
		return sp.Commit(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = pgx.BeginFunc(ctx, d.App, func(tx pgx.Tx) error {
		if db.AfterCommit(tx, func() {}) {
			t.Error("a transaction not from InTenantTx accepted an after-commit function")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
