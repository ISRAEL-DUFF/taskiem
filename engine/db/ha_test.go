package db_test

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

func setVar(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, name string) error {
	_, err := tx.Exec(ctx, `INSERT INTO variables (tenant_id, environment, name, value) VALUES ($1, 'prod', $2, '1')`, tenant, name)
	return err
}

func vars(t *testing.T, d *dbtest.DB, tenant uuid.UUID, name string) int {
	t.Helper()
	var n int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM variables WHERE tenant_id = $1 AND name = $2`, tenant, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTransient(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&pgconn.PgError{Code: "25006"}, true},
		{&pgconn.PgError{Code: "57P01"}, true},
		{&pgconn.PgError{Code: "08006"}, true},
		{&pgconn.PgError{Code: "40001"}, true},
		{&pgconn.PgError{Code: "23505"}, false}, // unique violation: the statement, not the server
		{&pgconn.PgError{Code: "42501"}, false},
		{io.ErrUnexpectedEOF, true},
		{context.Canceled, false},
		{errors.New("no StepScheduled"), false},
		{nil, false},
	} {
		if got := db.Transient(c.err); got != c.want {
			t.Errorf("Transient(%v) = %v", c.err, got)
		}
	}
}

// Connections dropped mid-transaction (a primary failing over): the
// transaction is retried on a new connection and lands once; without the
// retry it fails.
func TestRetryThroughConnectionDrop(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	px := d.NewProxy(t)
	pool := px.Pool(t, d, "taskiem_app", 4, nil)

	err := db.InTenantTx(ctx, pool, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		px.Cut()
		return setVar(ctx, tx, tn.ID, "plain")
	})
	if err == nil || !db.Transient(err) {
		t.Fatalf("without retry: %v", err)
	}

	var attempts atomic.Int32
	err = db.InTenantTxRetry(ctx, pool, []uuid.UUID{tn.ID}, false, func(tx pgx.Tx) error {
		if attempts.Add(1) == 1 {
			px.Cut()
		}
		return setVar(ctx, tx, tn.ID, "retried")
	})
	if err != nil || attempts.Load() != 2 || vars(t, d, tn.ID, "retried") != 1 {
		t.Fatalf("retry: %v after %d attempts, %d rows", err, attempts.Load(), vars(t, d, tn.ID, "retried"))
	}

	// The database refusing connections for a while (the failover itself),
	// then coming back.
	px.SetDown(true)
	go func() { time.Sleep(700 * time.Millisecond); px.SetDown(false) }()
	if err := db.InTenantTxRetry(ctx, pool, []uuid.UUID{tn.ID}, false, func(tx pgx.Tx) error { return setVar(ctx, tx, tn.ID, "after_outage") }); err != nil {
		t.Fatal(err)
	}
	if vars(t, d, tn.ID, "after_outage") != 1 {
		t.Error("not written after the outage")
	}

	// A statement error is not retried.
	attempts.Store(0)
	err = db.InTenantTxRetry(ctx, pool, []uuid.UUID{tn.ID}, true, func(tx pgx.Tx) error {
		attempts.Add(1)
		return setVar(ctx, tx, tn.ID, "retried") // duplicate key
	})
	if err == nil || attempts.Load() != 1 {
		t.Errorf("statement error: %v after %d attempts", err, attempts.Load())
	}

	// Past the window it gives up.
	old := db.RetryWindow
	db.RetryWindow = 300 * time.Millisecond
	defer func() { db.RetryWindow = old }()
	px.SetDown(true)
	start := time.Now()
	err = db.InTenantTxRetry(ctx, pool, []uuid.UUID{tn.ID}, false, func(tx pgx.Tx) error { return setVar(ctx, tx, tn.ID, "never") })
	px.SetDown(false)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("down past the window: %v after %s", err, time.Since(start))
	}
}

// A connection that lands on a read-only server (a standby, or the old
// primary come back as one) fails with read_only_sql_transaction; the pool
// is reset and the retry finds the read-write server.
func TestRetryOffAStandby(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	cfg, err := pgxpool.ParseConfig(d.DSN)
	if err != nil {
		t.Fatal(err)
	}
	var connects atomic.Int32
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		if _, err := c.Exec(ctx, "SET ROLE taskiem_app"); err != nil {
			return err
		}
		if connects.Add(1) == 1 { // the first server is a standby
			_, err := c.Exec(ctx, "SET default_transaction_read_only = on")
			return err
		}
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.InTenantTxRetry(ctx, pool, []uuid.UUID{tn.ID}, false, func(tx pgx.Tx) error { return setVar(ctx, tx, tn.ID, "rw") }); err != nil {
		t.Fatal(err)
	}
	if vars(t, d, tn.ID, "rw") != 1 || connects.Load() < 2 {
		t.Errorf("rows %d, connections %d", vars(t, d, tn.ID, "rw"), connects.Load())
	}
}

// A connection cut around COMMIT: the client cannot tell whether it
// committed (cut before it reached the server: nothing landed; after:
// it landed), so a non-idempotent transaction is not run again
// (ErrCommitUnknown) and lands at most once, while an idempotent one is
// retried, harmlessly.
func TestRetryAtCommit(t *testing.T) {
	d := dbtest.New(t)
	tn := d.SeedTenant(t, nil)
	px := d.NewProxy(t)
	pool := px.Pool(t, d, "taskiem_app", 4, nil)

	var attempts atomic.Int32
	err := db.InTenantTxRetry(ctx, pool, []uuid.UUID{tn.ID}, false, func(tx pgx.Tx) error {
		attempts.Add(1)
		defer px.CutAtCommit("before")
		return setVar(ctx, tx, tn.ID, "before")
	})
	if !errors.Is(err, db.ErrCommitUnknown) || vars(t, d, tn.ID, "before") != 0 || attempts.Load() != 1 {
		t.Fatalf("cut before commit: %v, %d rows, %d attempts", err, vars(t, d, tn.ID, "before"), attempts.Load())
	}

	attempts.Store(0)
	err = db.InTenantTxRetry(ctx, pool, []uuid.UUID{tn.ID}, false, func(tx pgx.Tx) error {
		attempts.Add(1)
		defer px.CutAtCommit("after")
		return setVar(ctx, tx, tn.ID, "after")
	})
	if !errors.Is(err, db.ErrCommitUnknown) || attempts.Load() != 1 {
		t.Fatalf("cut after commit, not idempotent: %v after %d attempts", err, attempts.Load())
	}
	time.Sleep(200 * time.Millisecond)
	if n := vars(t, d, tn.ID, "after"); n != 1 {
		t.Fatalf("the commit landed %d times, want once", n)
	}

	attempts.Store(0)
	err = db.InTenantTxRetry(ctx, pool, []uuid.UUID{tn.ID}, true, func(tx pgx.Tx) error {
		if attempts.Add(1) == 1 {
			defer px.CutAtCommit("after")
		}
		_, err := tx.Exec(ctx, `INSERT INTO variables (tenant_id, environment, name, value) VALUES ($1, 'prod', 'upsert', '1')
			ON CONFLICT (tenant_id, environment, name) DO NOTHING`, tn.ID)
		return err
	})
	if err != nil || attempts.Load() != 2 || vars(t, d, tn.ID, "upsert") != 1 {
		t.Fatalf("idempotent: %v after %d attempts, %d rows", err, attempts.Load(), vars(t, d, tn.ID, "upsert"))
	}
}
