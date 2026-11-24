package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00468 and 00469 (one customer across the app and WhatsApp) go up, down
// and up again: Down drops the link, the person's copy's line, the chat
// link's WhatsApp choice and the three definers, and puts 00456's way back
// in place.
func TestOneCustomerMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 469); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname IN ('customer_conversations_linked', 'customer_conversations_whatsapp_subject',
		'customer_conversations_link_due')`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("indexes: %d %v", n, err)
	}
	if _, err := p.DownTo(ctx, 467); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('taskiem_customer_link_person', 'taskiem_customer_link_phone', 'taskiem_customer_link_due')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("definers after Down: %d %v", n, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE (table_name, column_name) IN
		(('customer_conversations', 'linked_to'), ('person_messages', 'line'), ('customer_chat_link_settings', 'link_whatsapp'))`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("columns after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 469); err != nil {
		t.Fatal(err)
	}
}

// 00470 (the review fixes) goes up, down and up again: Down drops the
// stale-link definer and its column grants, and puts back the sweeps'
// definers without their guard.
func TestOneCustomerReviewMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 470); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	langs := func() string {
		t.Helper()
		var s string
		if err := conn.QueryRow(ctx, `SELECT string_agg(p.proname || ':' || l.lanname, ',' ORDER BY p.proname) FROM pg_proc p JOIN pg_language l ON l.oid = p.prolang
			WHERE p.proname IN ('taskiem_customer_link_due', 'taskiem_customer_link_stale', 'taskiem_customer_waiting')`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := langs(); got != "taskiem_customer_link_due:plpgsql,taskiem_customer_link_stale:plpgsql,taskiem_customer_waiting:plpgsql" {
		t.Fatalf("up: %s", got)
	}
	if _, err := p.DownTo(ctx, 469); err != nil {
		t.Fatal(err)
	}
	if got := langs(); got != "taskiem_customer_link_due:sql,taskiem_customer_waiting:sql" {
		t.Fatalf("down: %s", got)
	}
	if _, err := p.UpTo(ctx, 470); err != nil {
		t.Fatal(err)
	}
}
