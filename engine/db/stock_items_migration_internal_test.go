package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00501 (stock items apart from products) goes up, down and up again: its
// Down drops the stock items and their links, puts back the movements of
// the catalogue table and storefronts.keeps_stock, and its Up makes them
// again.
func TestStockItemsMigrationUpDownUp(t *testing.T) {
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
	type st struct {
		tables     int
		keepsStock int
		itemID     int
	}
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM pg_tables WHERE tablename IN ('stock_items', 'product_stock')),
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'storefronts' AND column_name = 'keeps_stock'),
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'stock_movements' AND column_name = 'item_id')`).Scan(&s.tables, &s.keepsStock, &s.itemID); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 501); err != nil {
			t.Fatal(err)
		}
		if s := state(); s != (st{tables: 2}) {
			t.Fatalf("round %d, up: %+v", round, s)
		}
		if _, err := p.DownTo(ctx, 500); err != nil {
			t.Fatal(err)
		}
		if s := state(); s != (st{keepsStock: 1, itemID: 1}) {
			t.Fatalf("round %d, down: %+v", round, s)
		}
	}
}
