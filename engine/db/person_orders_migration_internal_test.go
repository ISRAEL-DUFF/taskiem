package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00465 (Orders on You and saved addresses) goes up, down and up again:
// its Down drops the addresses and the two definers and puts the phone
// orders' functions back as 00458 left them.
func TestPersonOrdersMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 465); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ('00000000-0000-0000-0000-0000000000b1', 'tolu@x.test', 'Tolu')`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO person_addresses (id, person_id, label, line, is_default)
		VALUES (gen_random_uuid(), '00000000-0000-0000-0000-0000000000b1', 'Home', '12 Allen Avenue', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 464); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('taskiem_person_orders', 'taskiem_person_order_enter', 'taskiem_person_order_ids')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("definers after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 465); err != nil {
		t.Fatal(err)
	}
}
