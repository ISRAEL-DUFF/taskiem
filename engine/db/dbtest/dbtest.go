// Package dbtest creates a fresh, migrated database per test from
// TASKIEM_TEST_DATABASE_URL (a superuser DSN). Tests skip when it is unset.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect %s: %v", EnvVar, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	admin.Close(ctx)

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	dsn := u.String()
	if _, err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	d := &DB{DSN: dsn, Admin: pool(t, dsn, ""), App: pool(t, dsn, "taskiem_app"), Dispatch: pool(t, dsn, "taskiem_dispatch")}
	t.Cleanup(func() {
		d.Admin.Close()
		d.App.Close()
		d.Dispatch.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if c, err := pgx.Connect(ctx, base); err == nil {
			_, _ = c.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(ctx)
		}
	})
	return d
}

func pool(t testing.TB, dsn, role string) *pgxpool.Pool {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 32
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
