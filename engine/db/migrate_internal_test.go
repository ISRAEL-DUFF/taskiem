package db

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"strings"
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

// concurrentIndex is a migration building an index on things CONCURRENTLY,
// as CONTRIBUTING.md asks for large tables.
var concurrentIndex = fstest.MapFS{
	"00001_one.sql": {Data: []byte("-- +goose Up\nCREATE TABLE things (id int, name text);\n")},
	"00002_index.sql": {Data: []byte("-- +goose NO TRANSACTION\n-- A comment.\n\n-- +goose Up\nCREATE UNIQUE INDEX CONCURRENTLY things_name ON things (name);\n\n" +
		"-- +goose Down\nDROP INDEX CONCURRENTLY IF EXISTS things_name;\n")},
}

func indexValid(t *testing.T, dsn, name string) bool {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	var valid bool
	if err := c.QueryRow(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid = $1::regclass`, name).Scan(&valid); err != nil {
		t.Fatalf("index %s: %v", name, err)
	}
	return valid
}

// TestConcurrentIndexWaitsForOldTransactions: a concurrent index build
// waits for every transaction older than it, whatever table they read, and
// that wait is a lock wait. It blocks no query, so the lock timeout does
// not apply to it: a long report running during the migration delays it,
// rather than failing it and leaving an invalid index behind.
func TestConcurrentIndexWaitsForOldTransactions(t *testing.T) {
	dsn := emptyDatabase(t)
	ctx := context.Background()
	if _, err := migrate(ctx, dsn, DefaultLockTimeout, fstest.MapFS{"00001_one.sql": concurrentIndex["00001_one.sql"]}); err != nil {
		t.Fatal(err)
	}
	holder, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close(ctx) }()
	tx, err := holder.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT count(*) FROM pg_class"); err != nil { // a snapshot, on another table
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(1500 * time.Millisecond)
		_ = tx.Rollback(ctx)
	}()
	_, err = migrate(ctx, dsn, 300*time.Millisecond, concurrentIndex)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if !indexValid(t, dsn, "things_name") {
		t.Error("things_name is not valid")
	}
}

// TestFailedConcurrentIndexIsRetried: a concurrent build that failed
// leaves an invalid index of the same name; running the migration again
// rebuilds it instead of failing on "already exists" every time.
func TestFailedConcurrentIndexIsRetried(t *testing.T) {
	dsn := emptyDatabase(t)
	ctx := context.Background()
	if _, err := migrate(ctx, dsn, DefaultLockTimeout, fstest.MapFS{"00001_one.sql": concurrentIndex["00001_one.sql"]}); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if _, err := c.Exec(ctx, "INSERT INTO things VALUES (1, 'a'), (2, 'a')"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(ctx, "CREATE UNIQUE INDEX CONCURRENTLY things_name ON things (name)"); err == nil {
		t.Fatal("the duplicate did not fail the build")
	}
	if indexValid(t, dsn, "things_name") {
		t.Fatal("the failed build left a valid index")
	}
	if _, err := c.Exec(ctx, "DELETE FROM things WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := migrate(ctx, dsn, DefaultLockTimeout, concurrentIndex); err != nil {
		t.Fatal(err)
	}
	if !indexValid(t, dsn, "things_name") {
		t.Error("things_name is still invalid")
	}
}

// TestConcurrentIndexMigrationsAreRecognised: every migration that builds
// an index CONCURRENTLY is one migrate runs without the lock timeout and
// with a drop before each build (concurrent.go). One that mixes in other
// statements would run as written, with neither.
func TestConcurrentIndexMigrationsAreRecognised(t *testing.T) {
	sub := mustSub(migrations, "migrations")
	paths, err := fs.Glob(sub, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range paths {
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			t.Fatal(err)
		}
		up := string(b)
		if i := strings.Index(up, "-- +goose Down"); i >= 0 {
			up = up[:i]
		}
		if !strings.Contains(up, "INDEX CONCURRENTLY") {
			continue
		}
		n++
		src, builds, ok := concurrentMigration(string(b))
		if !ok {
			t.Errorf("%s builds an index CONCURRENTLY but is not a concurrent index migration (NO TRANSACTION, nothing else in Up)", p)
			continue
		}
		for _, bl := range builds {
			if !strings.Contains(src, "DROP INDEX CONCURRENTLY IF EXISTS "+bl.name+";\nCREATE") {
				t.Errorf("%s: no drop before building %s:\n%s", p, bl.name, src)
			}
		}
	}
	if n == 0 {
		t.Error("no concurrent index migrations found")
	}
	if _, _, ok := concurrentMigration("-- +goose NO TRANSACTION\n-- +goose Up\nCREATE INDEX CONCURRENTLY a ON b (c);\nALTER TABLE b ADD COLUMN d int;\n"); ok {
		t.Error("a migration with an ALTER TABLE counted as a concurrent index migration")
	}
}
