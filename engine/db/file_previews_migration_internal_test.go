package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00780 (the pictures of PDFs' first pages) goes up, down and up again.
func TestFilePreviewsMigrationUpDownUp(t *testing.T) {
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
	has := func() bool {
		t.Helper()
		var n int
		if err := conn.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_policies WHERE policyname = 'member_preview_insert')
			+ (SELECT count(*) FROM pg_indexes WHERE indexname = 'person_files_one_preview')
			+ (SELECT count(*) FROM pg_constraint WHERE conname IN ('people_files_kind_check', 'group_files_kind_check', 'person_files_kind_check')
				AND pg_get_constraintdef(oid) LIKE '%preview%')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 5
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 780); err != nil {
			t.Fatal(err)
		}
		if !has() {
			t.Fatalf("round %d, up: the previews' kinds, policy or index missing", round)
		}
		if _, err := p.DownTo(ctx, 770); err != nil {
			t.Fatal(err)
		}
		if has() {
			t.Fatalf("round %d, down: the previews' kinds kept", round)
		}
	}
}
