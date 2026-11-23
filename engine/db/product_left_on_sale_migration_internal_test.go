package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00508 (how many are left, only of what a page sells) goes up, down and up again.
func TestProductLeftOnSaleMigrationUpDownUp(t *testing.T) {
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
	has := func() bool {
		t.Helper()
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*)::int FROM pg_proc WHERE proname = 'taskiem_product_left' AND prosrc LIKE '%on_sale%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 508); err != nil {
			t.Fatal(err)
		}
		if !has() {
			t.Fatalf("round %d, up: not limited to what is on sale", round)
		}
		if _, err := p.DownTo(ctx, 507); err != nil {
			t.Fatal(err)
		}
		if has() {
			t.Fatalf("round %d, down: still limited", round)
		}
	}
}
