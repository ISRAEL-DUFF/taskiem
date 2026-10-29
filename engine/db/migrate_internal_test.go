package db

import (
	"context"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// emptyDatabase creates a database with nothing in it, dropped when the
// test ends (dbtest cannot be used here: it imports this package).
func emptyDatabase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("TASKIEM_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TASKIEM_TEST_DATABASE_URL not set; skipping database test")
	}
	ctx := context.Background()
	name := "taskiem_test_" + uuid.NewString()[:8]
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), base); err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = c.Close(context.Background())
		}
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

var testMigrations = fstest.MapFS{
	"00001_one.sql": {Data: []byte("-- +goose Up\nCREATE TABLE things (id int);\nSELECT pg_sleep(0.5);\n")},
	"00002_two.sql": {Data: []byte("-- +goose Up\nALTER TABLE things ADD COLUMN name text;\n")},
}

// TestConcurrentMigrationsTakeTurns: two migrate runs at once (a manual
// run beside the Job) take goose's session lock in turn, instead of both
// applying the same migration.
func TestConcurrentMigrationsTakeTurns(t *testing.T) {
	dsn := emptyDatabase(t)
	const runs = 8
	var wg sync.WaitGroup
	errs := make([]error, runs)
	applied := 0
	var mu sync.Mutex
	for i := range runs {
		wg.Go(func() {
			r, err := migrate(context.Background(), dsn, DefaultLockTimeout, testMigrations)
			mu.Lock()
			errs[i], applied = err, applied+len(r)
			mu.Unlock()
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil || applied != len(testMigrations) {
		t.Fatalf("applied %d: %v", applied, err)
	}
}

// TestMigrationsGiveUpOnALock: a migration that waits for a lock a live
// transaction holds fails after the lock timeout, instead of queueing
// every other query on that table behind it.
func TestMigrationsGiveUpOnALock(t *testing.T) {
	dsn := emptyDatabase(t)
	ctx := context.Background()
	if _, err := migrate(ctx, dsn, DefaultLockTimeout, fstest.MapFS{"00001_one.sql": testMigrations["00001_one.sql"]}); err != nil {
		t.Fatal(err)
	}
	holder, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close(ctx) }()
	tx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT * FROM things"); err != nil { // ACCESS SHARE, as a live query holds
		t.Fatal(err)
	}
	start := time.Now()
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err = migrate(wait, dsn, time.Second, testMigrations)
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "55P03" || time.Since(start) > 10*time.Second {
		t.Fatalf("after %s: %v", time.Since(start), err)
	}
}
