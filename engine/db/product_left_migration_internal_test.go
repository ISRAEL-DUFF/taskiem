package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00502 (how many are left of a product) goes up, down and up again: its
// Down drops taskiem_product_left and puts back the buyer's copied count,
// and its Up makes the function again and drops the copied count.
func TestProductLeftMigrationUpDownUp(t *testing.T) {
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
	state := func() (fn, leftCount int) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM pg_proc WHERE proname = 'taskiem_product_left'),
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'supplier_prices' AND column_name = 'left_count')`).Scan(&fn, &leftCount); err != nil {
			t.Fatal(err)
		}
		return fn, leftCount
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 502); err != nil {
			t.Fatal(err)
		}
		if fn, lc := state(); fn != 1 || lc != 0 {
			t.Fatalf("round %d, up: function %d, left_count %d", round, fn, lc)
		}
		if _, err := p.DownTo(ctx, 501); err != nil {
			t.Fatal(err)
		}
		if fn, lc := state(); fn != 0 || lc != 1 {
			t.Fatalf("round %d, down: function %d, left_count %d", round, fn, lc)
		}
	}
}
