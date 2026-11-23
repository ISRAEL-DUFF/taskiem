package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00503 (purchases on the server) goes up, down and up again: its Down
// drops the purchase lines, labels, both memories of a supplier's item and
// Things we have, puts back the order's JSON lines and the old item links,
// and its Up makes them again.
func TestPurchaseLinesMigrationUpDownUp(t *testing.T) {
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
		jsonLines  int
		labelID    int
		oldItemID  int
		linkTreats int
	}
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM pg_tables WHERE tablename IN ('purchase_lines', 'spend_labels', 'supplier_items', 'kept_things')),
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'purchase_orders' AND column_name = 'lines'),
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'money_entries' AND column_name = 'label_id'),
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'item_links' AND column_name = 'item_id'),
			(SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'item_links' AND column_name = 'treatment')`).
			Scan(&s.tables, &s.jsonLines, &s.labelID, &s.oldItemID, &s.linkTreats); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 503); err != nil {
			t.Fatal(err)
		}
		if s := state(); s != (st{tables: 4, labelID: 1, linkTreats: 1}) {
			t.Fatalf("round %d, up: %+v", round, s)
		}
		if _, err := p.DownTo(ctx, 502); err != nil {
			t.Fatal(err)
		}
		if s := state(); s != (st{jsonLines: 1, oldItemID: 1}) {
			t.Fatalf("round %d, down: %+v", round, s)
		}
	}
}
