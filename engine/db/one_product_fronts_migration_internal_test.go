package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00820 (forms that sell L2: a front may be of the One product kind) goes
// up, down and up again.
func TestOneProductFrontsMigrationUpDownUp(t *testing.T) {
	dsn := emptyDatabase(t)
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDB(*cfg)
	defer func() { _ = sqlDB.Close() }()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, mustSub(migrations, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	takes := func() bool {
		t.Helper()
		var def string
		if err := conn.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'storefront_fronts_kind_check'`).Scan(&def); err != nil {
			t.Fatal(err)
		}
		return strings.Contains(def, "one-product")
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 820); err != nil {
			t.Fatal(err)
		}
		if !takes() {
			t.Fatalf("round %d, up: a One product front refused", round)
		}
		if _, err := p.DownTo(ctx, 819); err != nil {
			t.Fatal(err)
		}
		if takes() {
			t.Fatalf("round %d, down: a One product front still taken", round)
		}
	}
}
