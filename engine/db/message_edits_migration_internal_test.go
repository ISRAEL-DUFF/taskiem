package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00790 (editing a sent message) goes up, down and up again: the edit
// columns, the edit history and the customer reach's new action come and go.
func TestMessageEditsMigrationUpDownUp(t *testing.T) {
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
	has := func() int {
		t.Helper()
		var n int
		if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM information_schema.columns WHERE column_name IN ('edited_at', 'edits', 'acted_at')
				AND table_name IN ('link_messages', 'people_messages', 'group_messages', 'customer_messages', 'person_messages'))
			+ (SELECT count(*) FROM pg_tables WHERE tablename = 'link_message_edits')
			+ (SELECT count(*) FROM pg_proc WHERE proname = 'taskiem_customer_reach' AND prosrc LIKE '%customer_chat.message.edit%')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 790); err != nil {
			t.Fatal(err)
		}
		if n := has(); n != 14 {
			t.Fatalf("round %d, up: %d of the edits' columns, table and action, want 14", round, n)
		}
		if _, err := p.DownTo(ctx, 780); err != nil {
			t.Fatal(err)
		}
		if n := has(); n != 0 {
			t.Fatalf("round %d, down: %d of them kept", round, n)
		}
	}
}
