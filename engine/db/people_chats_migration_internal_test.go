package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00471 (people chats) goes up, down and up again: Down drops the copies,
// keys, blocks, reports, the requests' ledger and the definers, the push
// kinds and settings, and puts back 00467's account deletion.
func TestPeopleChatsMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 471); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE tablename IN ('people_chats', 'people_messages', 'people_files', 'person_keys',
		'people_blocks', 'people_reports', 'people_requests', 'person_payout_accounts')`).Scan(&n); err != nil || n != 8 {
		t.Fatalf("tables: %d %v", n, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE prosrc LIKE '%people_requests%' AND proname = 'taskiem_person_delete'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("account deletion ends requests: %d %v", n, err)
	}
	if _, err := p.DownTo(ctx, 470); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname LIKE 'taskiem_people_%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("definers after Down: %d %v", n, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE prosrc LIKE '%people_requests%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("functions naming the ledger after Down: %d %v", n, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'person_notify' AND column_name IN ('people', 'requests')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("columns after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 471); err != nil {
		t.Fatal(err)
	}
}
