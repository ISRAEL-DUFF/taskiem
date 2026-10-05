// Package db holds the schema migrations and the helpers every role uses to
// run a transaction under a tenant scope.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies all pending migrations. It must run as the schema owner.
func Migrate(ctx context.Context, dsn string) ([]*goose.MigrationResult, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	sqlDB := stdlib.OpenDB(*cfg)
	defer func() { _ = sqlDB.Close() }()
	return migrate(ctx, sqlDB)
}

func migrate(ctx context.Context, sqlDB *sql.DB) ([]*goose.MigrationResult, error) {
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, mustSub(migrations, "migrations"))
	if err != nil {
		return nil, err
	}
	return p.Up(ctx)
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
