package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00590 (a stock item's barcode, device fields D2) goes up, down and up
// again: Up adds stock_items.code, its index and the app's grant; Down
// drops them. Nothing outside the business is given it: no other role may
// read it, and the public page's taskiem_product_left does not return it.
func TestStockItemCodeMigrationUpDownUp(t *testing.T) {
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
		column, index int
		appUpdate     bool
	}
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'stock_items' AND column_name = 'code'),
			(SELECT count(*)::int FROM pg_indexes WHERE indexname = 'stock_items_code'),
			EXISTS (SELECT 1 FROM information_schema.column_privileges WHERE table_name = 'stock_items' AND column_name = 'code'
			         AND grantee = 'taskiem_app' AND privilege_type = 'UPDATE')`).Scan(&s.column, &s.index, &s.appUpdate); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 590); err != nil {
			t.Fatal(err)
		}
		if s := state(); s != (st{column: 1, index: 1, appUpdate: true}) {
			t.Fatalf("round %d, up: %+v", round, s)
		}
		var others int
		var result string
		if err := conn.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM information_schema.column_privileges WHERE table_name = 'stock_items' AND column_name = 'code'
			  AND grantee IN ('taskiem_public', 'taskiem_dispatch', 'taskiem_safety')),
			(SELECT string_agg(pg_get_function_result(oid), ' ') FROM pg_proc WHERE proname = 'taskiem_product_left')`).Scan(&others, &result); err != nil {
			t.Fatal(err)
		}
		if others != 0 || result != "TABLE(product_id text, sales_left bigint)" {
			t.Fatalf("round %d: the code reaches outside the business: %d grants, %s", round, others, result)
		}
		if _, err := p.DownTo(ctx, 589); err != nil {
			t.Fatal(err)
		}
		if s := state(); s != (st{}) {
			t.Fatalf("round %d, down: %+v", round, s)
		}
	}
}
