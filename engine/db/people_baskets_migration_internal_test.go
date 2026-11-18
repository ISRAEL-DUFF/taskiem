package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00486 (ordering together) goes up, down and up again: up, the baskets,
// their lines and shares, the business's share pay links, the basket card
// type and the three definers are there; down, all is as 00484 left it
// (a basket card goes with its basket).
func TestPeopleBasketsMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 484); err != nil {
		t.Fatal(err)
	}
	type st struct{ tables, definers, basketCards int }
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM pg_class WHERE relname IN ('people_baskets', 'people_basket_lines', 'people_basket_shares', 'storefront_order_share_links') AND relkind = 'r'),
			(SELECT count(*)::int FROM pg_proc WHERE proname IN ('taskiem_basket_reach', 'taskiem_basket_enter', 'taskiem_basket_order')),
			(SELECT count(*)::int FROM pg_constraint WHERE conname IN ('people_messages_card_type_check', 'group_messages_card_type_check')
			   AND pg_get_constraintdef(oid) LIKE '%basket%')`).Scan(&s.tables, &s.definers, &s.basketCards); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 486); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.tables != 4 || s.definers != 3 || s.basketCards != 2 {
			t.Fatalf("up: %+v", s)
		}
		if _, err := p.DownTo(ctx, 484); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.tables != 0 || s.definers != 0 || s.basketCards != 0 {
			t.Fatalf("down: %+v", s)
		}
	}
	if _, err := p.UpTo(ctx, 486); err != nil {
		t.Fatal(err)
	}
}
