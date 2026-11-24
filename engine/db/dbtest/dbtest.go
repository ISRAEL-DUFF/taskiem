// Package dbtest creates a fresh, migrated database per test from
// TASKIEM_TEST_DATABASE_URL (a superuser DSN). Tests skip when it is unset.
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/israel-duff/taskiem/engine/db"
)

const EnvVar = "TASKIEM_TEST_DATABASE_URL"

// DB is a throwaway database with pools for each role.
type DB struct {
	DSN      string        // superuser DSN for this database
	Admin    *pgxpool.Pool // superuser: bypasses RLS; use only for setup and tampering tests
	App      *pgxpool.Pool // SET ROLE taskiem_app
	Dispatch *pgxpool.Pool // SET ROLE taskiem_dispatch
}

// New creates and migrates a database, and drops it when the test ends.
func New(t testing.TB) *DB {
	t.Helper()
	base := os.Getenv(EnvVar)
	if base == "" {
		t.Skipf("%s not set; skipping database test", EnvVar)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	name := "taskiem_test_" + uuid.NewString()[:8]
	tmpl, err := template(base)
	if err != nil {
		t.Fatalf("the migrated template: %v", err)
	}
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect %s: %v", EnvVar, err)
	}
	// A copy of the migrated template: a fraction of a second, where
	// migrating each test's database from nothing took seconds.
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+tmpl); err != nil {
		t.Fatal(err)
	}
	_ = admin.Close(ctx)
	dsn := dsnFor(t, base, name)

	// Small pools: CI's Postgres allows 100 connections and test packages run in parallel.
	d := &DB{DSN: dsn, Admin: pool(t, dsn, "", 4), App: pool(t, dsn, "taskiem_app", 16), Dispatch: pool(t, dsn, "taskiem_dispatch", 2)}
	t.Cleanup(func() {
		d.Admin.Close()
		d.App.Close()
		d.Dispatch.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if c, err := pgx.Connect(ctx, base); err == nil {
			_, _ = c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = c.Close(ctx)
		}
	})
	return d
}

// AppPool opens another pool as taskiem_app, closed when the test ends:
// for components that must not share connections with their callers.
func (d *DB) AppPool(t testing.TB, size int32) *pgxpool.Pool {
	p := pool(t, d.DSN, "taskiem_app", size)
	t.Cleanup(p.Close)
	return p
}

func pool(t testing.TB, dsn, role string, size int32) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = size
	if role != "" {
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET ROLE "+role)
			return err
		}
	}
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// PublicPool opens a pool as taskiem_public, the public host's role,
// closed when the test ends.
func (d *DB) PublicPool(t testing.TB, size int32) *pgxpool.Pool {
	p := pool(t, d.DSN, "taskiem_public", size)
	t.Cleanup(p.Close)
	return p
}

// Fixture ids for one tenant's minimal object graph.
type Tenant struct {
	ID, WorkflowID uuid.UUID
}

// SeedTenant creates a tenant with one published workflow version, through
// the app role so RLS checks apply to the seed itself.
func (d *DB) SeedTenant(t testing.TB, parent *uuid.UUID) Tenant {
	t.Helper()
	ctx := context.Background()
	tn := Tenant{ID: uuid.Must(uuid.NewV7()), WorkflowID: uuid.Must(uuid.NewV7())}
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, parent_id, name, plan_id) VALUES ($1, $2, 't', $3)`,
			tn.ID, parent, uuid.New()); err != nil {
			return fmt.Errorf("tenant: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, tenant_id, name, created_by) VALUES ($1, $2, 'wf', $3)`,
			tn.WorkflowID, tn.ID, uuid.New()); err != nil {
			return fmt.Errorf("workflow: %w", err)
		}
		_, err := tx.Exec(ctx, `INSERT INTO workflow_versions (workflow_id, version, tenant_id, definition, digest, state)
			VALUES ($1, 1, $2, '{"schema":"wd/v1"}', sha256('x'), 'published')`, tn.WorkflowID, tn.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tn
}

// StartRun creates a run and appends RunStarted.
func (d *DB) StartRun(t testing.TB, tn Tenant) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	runID := uuid.Must(uuid.NewV7())
	err := db.InTenantTx(ctx, d.App, []uuid.UUID{tn.ID}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO runs (id, tenant_id, workflow_id, version, environment, started_at)
			VALUES ($1, $2, $3, 1, 'prod', now())`, runID, tn.ID, tn.WorkflowID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT taskiem_append_event($1, 'RunStarted', NULL, NULL, '{}')`, runID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return runID
}

func dsnFor(t testing.TB, base, name string) string {
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// The template every test database is copied from: migrated once, named
// by the migrations' digest, so a change to any migration builds a new
// one. Test packages run in parallel, each its own process: one builds it
// under an advisory lock while the others wait, and then all copy it.
// Nothing connects to it once it is built (a copy needs that).
var tmplOnce struct {
	sync.Once
	name string
	err  error
}

const tmplPrefix = "taskiem_tmpl_"

func template(base string) (string, error) {
	tmplOnce.Do(func() { tmplOnce.name, tmplOnce.err = buildTemplate(base) })
	return tmplOnce.name, tmplOnce.err
}

func buildTemplate(base string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	name := tmplPrefix + db.MigrationsDigest()[:16]
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		return "", err
	}
	defer func() { _ = admin.Close(context.Background()) }()
	// One process builds; the lock is the session's, freed with it.
	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock(7342019)`); err != nil {
		return "", err
	}
	defer func() { _, _ = admin.Exec(context.Background(), `SELECT pg_advisory_unlock(7342019)`) }()
	var have bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&have); err != nil {
		return "", err
	}
	if have {
		return name, nil
	}
	// Built under another name and renamed when whole: a build cut short
	// is never copied.
	build := name + "_b"
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+build+" WITH (FORCE)"); err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+build); err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/" + build
	if err := migrate(ctx, u.String()); err != nil {
		return "", fmt.Errorf("migrate: %w", err)
	}
	// Its connections are closed with the migration; any left would stop
	// the rename and every copy.
	if _, err := admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, build); err != nil {
		return "", err
	}
	if _, err := admin.Exec(ctx, "ALTER DATABASE "+build+" RENAME TO "+name); err != nil {
		return "", err
	}
	// Templates of migrations since changed are no longer used here.
	rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE $1 AND datname <> $2`, tmplPrefix+"%", name)
	if err != nil {
		return name, nil
	}
	old, _ := pgx.CollectRows(rows, pgx.RowTo[string])
	for _, o := range old {
		_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+o)
	}
	return name, nil
}

// migrate migrates a test database. Roles belong to the whole server, so
// two packages migrating their own databases at once can both try to
// create the same role, and one loses with a unique violation on
// pg_authid. That migration rolled back; running it again finds the role
// and carries on.
func migrate(ctx context.Context, dsn string) error {
	for attempt := 1; ; attempt++ {
		_, err := db.Migrate(ctx, dsn)
		if err == nil || attempt == 5 || !roleRace(err) {
			return err
		}
	}
}

func roleRace(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "pg_authid_rolname_index"
}
