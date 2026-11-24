package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00504 (review fixes to purchases) goes up, down and up again: its Down
// drops the receipt keys, the line's name key and to_confirm, and puts
// back supplier_items' old CHECK on the name key; its Up makes them again.
func TestPurchaseReceiptsMigrationUpDownUp(t *testing.T) {
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
		receipts, nameKey, toConfirm, oldCheck, newCheck int
		forced                                           bool
	}
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM pg_tables WHERE tablename = 'purchase_receipts'),
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'purchase_lines' AND column_name = 'name_key'),
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'purchase_lines' AND column_name = 'to_confirm'),
			(SELECT count(*)::int FROM pg_constraint WHERE conname = 'supplier_items_name_key_check'),
			(SELECT count(*)::int FROM pg_constraint WHERE conname = 'supplier_items_name_key_form'),
			COALESCE((SELECT relrowsecurity AND relforcerowsecurity FROM pg_class WHERE relname = 'purchase_receipts'), false)`).
			Scan(&s.receipts, &s.nameKey, &s.toConfirm, &s.oldCheck, &s.newCheck, &s.forced); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 504); err != nil {
			t.Fatal(err)
		}
		if s := state(); s != (st{receipts: 1, nameKey: 1, toConfirm: 1, newCheck: 1, forced: true}) {
			t.Fatalf("round %d, up: %+v", round, s)
		}
		if _, err := p.DownTo(ctx, 503); err != nil {
			t.Fatal(err)
		}
		if s := state(); s != (st{oldCheck: 1}) {
			t.Fatalf("round %d, down: %+v", round, s)
		}
	}
}
