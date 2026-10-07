package db_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// replica stands in for a streaming replica: the same database through a
// proxy, read-only at the session level (as a hot standby is), with its
// own application_name so tests can tell where a query ran.
func replica(t *testing.T, d *dbtest.DB) (*db.Replica, *dbtest.Proxy) {
	t.Helper()
	px := d.NewProxy(t)
	pool := px.Pool(t, d, "taskiem_app", 4, map[string]string{"application_name": "replica", "default_transaction_read_only": "on"})
	r := &db.Replica{Pool: pool, Primary: d.App, MaxLag: 5 * time.Second}
	if err := r.Check(ctx); err != nil || !r.InUse() {
		t.Fatalf("replica check: %v (in use %v, lag %v)", err, r.InUse(), r.Lag())
	}
	return r, px
}

func where(t *testing.T, primary *db.Replica, tenant uuid.UUID) string {
	t.Helper()
	var app string
	if err := db.ReadTx(ctx, primary.Primary, primary, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_setting('application_name')`).Scan(&app)
	}); err != nil {
		t.Fatal(err)
	}
	if app == "replica" {
		return "replica"
	}
	return "primary"
}

// Reads go to the replica while it keeps up, under the same row-level
// security as the primary, and can never write.
func TestReplicaReadsUnderRLS(t *testing.T) {
	d := dbtest.New(t)
	a, b := d.SeedTenant(t, nil), d.SeedTenant(t, nil)
	runA := d.StartRun(t, a)
	r, _ := replica(t, d)
	if got := where(t, r, a.ID); got != "replica" {
		t.Fatalf("read ran on the %s", got)
	}
	countRuns := func(tenant uuid.UUID) int {
		var n int
		if err := db.ReadTx(ctx, d.App, r, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM runs WHERE id = $1`, runA).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if countRuns(a.ID) != 1 || countRuns(b.ID) != 0 {
		t.Error("row-level security does not hold on the replica")
	}
	// The role on the replica is taskiem_app, not the login role.
	var role string
	if err := db.ReadTx(ctx, d.App, r, []uuid.UUID{a.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_user`).Scan(&role)
	}); err != nil || role != "taskiem_app" {
		t.Errorf("replica role %q (%v)", role, err)
	}
	// A write inside a read fails, on the replica and on the primary.
	write := func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO variables (tenant_id, environment, name, value) VALUES ($1, 'prod', 'x', '1')`, a.ID)
		return err
	}
	if err := db.ReadTx(ctx, d.App, r, []uuid.UUID{a.ID}, write); sqlState(err) != "25006" || !r.InUse() {
		t.Errorf("write through the replica: %v (replica still in use: %v)", err, r.InUse())
	}
	if err := db.ReadTx(ctx, d.App, nil, []uuid.UUID{a.ID}, write); sqlState(err) != "25006" {
		t.Errorf("write through a read on the primary: %v", err)
	}
	if err := db.ReadTx(ctx, d.App, r, nil, write); err == nil {
		t.Error("empty scope accepted")
	}
}

// Lagging beyond the bound or unreachable, the replica is set aside and
// reads go to the primary; caught up, it is used again.
func TestReplicaFallsBack(t *testing.T) {
	d := dbtest.New(t)
	a := d.SeedTenant(t, nil)
	r, px := replica(t, d)

	// Lag: the heartbeat the replica sees is a minute old.
	r.Beat = func(context.Context) error { return nil }
	if _, err := d.Admin.Exec(ctx, `UPDATE db_heartbeat SET beat_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if err := r.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if r.InUse() || r.Lag() < 59 {
		t.Fatalf("lagging replica in use %v, lag %v", r.InUse(), r.Lag())
	}
	if got := where(t, r, a.ID); got != "primary" {
		t.Errorf("lagging: read ran on the %s", got)
	}
	r.Beat = nil
	if err := r.Check(ctx); err != nil || !r.InUse() || r.Lag() > 1 {
		t.Fatalf("caught up: %v, in use %v, lag %v", err, r.InUse(), r.Lag())
	}

	// Down: a query that fails on the replica is run on the primary.
	px.SetDown(true)
	if got := where(t, r, a.ID); got != "primary" {
		t.Errorf("replica down: read ran on the %s", got)
	}
	if r.InUse() {
		t.Error("a failed replica stays in use")
	}
	if err := r.Check(ctx); err == nil || r.InUse() || r.Lag() != -1 {
		t.Errorf("check while down: %v, in use %v, lag %v", err, r.InUse(), r.Lag())
	}
	px.SetDown(false)
	if err := r.Check(ctx); err != nil || !r.InUse() {
		t.Errorf("back: %v, in use %v", err, r.InUse())
	}
}

// Only staleness-tolerant views use the replica: run lists and the
// dashboard. Nothing that decides, leases or writes may call ReadTx; a new
// caller must be added here on purpose.
func TestReadReplicaCallers(t *testing.T) {
	root := filepath.Join("..", "..")
	call := regexp.MustCompile(`\b(ReadTx|readTx)\(`)
	got := map[string]int{}
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() && (e.Name() == "node_modules" || e.Name() == "web" || strings.HasPrefix(e.Name(), ".")) && path != root {
			return filepath.SkipDir
		}
		if e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") || strings.HasPrefix(line, "func ") {
				continue // comments and the definitions themselves
			}
			if call.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				got[filepath.ToSlash(rel)]++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"api/runs.go":             1, // listRuns
		"api/dashboard.go":        1, // dashboard
		"api/server.go":           1, // readTx -> Store.ReadTx
		"engine/runtime/pools.go": 1, // Store.ReadTx -> db.ReadTx
	}
	var files []string
	for f := range got {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if want[f] != got[f] {
			t.Errorf("%s calls ReadTx %d times, want %d: reads from the replica must tolerate staleness and never lead to a write", f, got[f], want[f])
		}
	}
	for f, n := range want {
		if got[f] != n {
			t.Errorf("%s: %d calls, want %d", f, got[f], n)
		}
	}
}
