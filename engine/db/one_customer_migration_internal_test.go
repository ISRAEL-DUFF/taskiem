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
