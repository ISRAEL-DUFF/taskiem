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

// 00467 (Orders and account review fixes) goes up, down and up again: its
// Down drops the person's photo and settings, the deletion and privacy
// definers, the users trigger and the marks, and puts 00465's functions
// back.
func TestPersonOrdersReviewFixesMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 467); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, q := range []string{
		`INSERT INTO users (id, email, name) VALUES ('00000000-0000-0000-0000-0000000000b2', 'ada@x.test', 'Ada')`,
		`INSERT INTO person_notify (person_id, offers) VALUES ('00000000-0000-0000-0000-0000000000b2', false)`,
		`INSERT INTO person_photos (person_id, type, data) VALUES ('00000000-0000-0000-0000-0000000000b2', 'image/jpeg', '\xffd8')`,
		`INSERT INTO phone_codes (number, purpose, user_id, code_hash, sent_via, expires_at)
		 VALUES ('+2348030000001', 'delete', '00000000-0000-0000-0000-0000000000b2', sha256('x'), 'sms', now())`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var payable bool
	if err := conn.QueryRow(ctx, `SELECT taskiem_order_payable_now('cancelled', 'at_order', 500)`).Scan(&payable); err != nil || payable {
		t.Fatalf("a cancelled order payable: %v %v", payable, err)
	}
	if _, err := p.DownTo(ctx, 465); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('taskiem_person_delete', 'taskiem_person_businesses',
		'taskiem_order_payable_now', 'taskiem_person_orders_release', 'taskiem_users_before_delete')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("functions after Down: %d %v", n, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('taskiem_person_orders', 'taskiem_person_order_enter', 'taskiem_person_order_ids')`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("00465's functions after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 467); err != nil {
		t.Fatal(err)
	}
}
