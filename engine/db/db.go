// Package db holds the schema migrations and the helpers every role uses to
// run a transaction under a tenant scope.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DefaultLockTimeout bounds how long each statement of a migration waits
// for a lock (TASKIEM_MIGRATE_LOCK_TIMEOUT): a migration queued behind a
// long transaction would otherwise block every query on that table, while
// the old release's pods are live. It fails instead, and is retried.
const DefaultLockTimeout = 10 * time.Second

// Migrate applies all pending migrations. It must run as the schema owner.
func Migrate(ctx context.Context, dsn string) ([]*goose.MigrationResult, error) {
	return MigrateWith(ctx, dsn, DefaultLockTimeout)
}

// MigrateWith is Migrate with another lock timeout; 0 waits for ever.
// Concurrent runs take turns, under goose's session-level advisory lock.
func MigrateWith(ctx context.Context, dsn string, lockTimeout time.Duration) ([]*goose.MigrationResult, error) {
	return migrate(ctx, dsn, lockTimeout, mustSub(migrations, "migrations"))
}

func migrate(ctx context.Context, dsn string, lockTimeout time.Duration, fsys fs.FS) ([]*goose.MigrationResult, error) {
	fsys, builds, err := concurrentMigrations(fsys)
	if err != nil {
		return nil, err
	}
	p, sqlDB, err := provider(dsn, lockTimeout, fsys)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sqlDB.Close() }()
	// Concurrent index migrations (concurrent.go) run without the lock
	// timeout, on connections of their own: pending migrations are applied
	// in runs of one kind or the other, in order.
	var concurrent *goose.Provider
	var all []*goose.MigrationResult
	for idle := 0; ; {
		st, err := p.Status(ctx)
		if err != nil {
			return all, err
		}
		var pending []*goose.Source
		for _, s := range st {
			if s.State == goose.StatePending {
				pending = append(pending, s.Source)
			}
		}
		if len(pending) == 0 {
			return all, nil
		}
		kind := func(s *goose.Source) bool { _, ok := builds[s.Path]; return ok }
		run, last := kind(pending[0]), pending[0].Version
		for _, s := range pending[1:] {
			if kind(s) != run {
				break
			}
			last = s.Version
		}
		by := p
		if run {
			for _, s := range pending {
				if s.Version > last {
					break
				}
				if err := checkIndexNames(ctx, sqlDB, s.Path, builds[s.Path]); err != nil {
					return all, err
				}
			}
			if concurrent == nil {
				var cdb *sql.DB
				if concurrent, cdb, err = provider(dsn, 0, fsys); err != nil {
					return all, err
				}
				defer func() { _ = cdb.Close() }()
			}
			by = concurrent
		}
		res, err := by.UpTo(ctx, last)
		all = append(all, res...)
		if err != nil {
			return all, err
		}
		// Nothing applied: another run did it while this one waited for
		// the lock. Twice in a row would be a loop.
		if len(res) > 0 {
			idle = 0
		} else if idle++; idle > 1 {
			return all, fmt.Errorf("migration %d stays pending", pending[0].Version)
		}
	}
}

// provider is a goose provider whose connections have lockTimeout.
func provider(dsn string, lockTimeout time.Duration, fsys fs.FS) (*goose.Provider, *sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, nil, err
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["lock_timeout"] = strconv.FormatInt(lockTimeout.Milliseconds(), 10)
	sqlDB := stdlib.OpenDB(*cfg)
	// Another run holding the lock is waited for, checking every second,
	// for up to 5 minutes.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, err
	}
	return p, sqlDB, nil
}

// Scope renders tenant ids as the value of app.tenant_scope.
func Scope(tenants ...uuid.UUID) string {
	parts := make([]string, len(tenants))
	for i, t := range tenants {
		parts[i] = t.String()
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// InTenantTx runs fn in a transaction whose RLS scope is exactly the given
// tenants. It is the only way application code should touch tenant data.
func InTenantTx(ctx context.Context, pool *pgxpool.Pool, tenants []uuid.UUID, fn func(pgx.Tx) error) error {
	if len(tenants) == 0 {
		return fmt.Errorf("db: empty tenant scope")
	}
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_scope', $1, true)", Scope(tenants...)); err != nil {
			return err
		}
		return fn(tx)
	})
}
