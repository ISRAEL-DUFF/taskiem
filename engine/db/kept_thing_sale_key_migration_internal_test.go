package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00506 (a thing's sale keeps its key) goes up, down and up again.
func TestKeptThingSaleKeyMigrationUpDownUp(t *testing.T) {
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
		if err := conn.QueryRow(ctx, `SELECT count(*)::int FROM information_schema.columns WHERE table_name = 'kept_things' AND column_name = 'sale_key'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 506); err != nil {
			t.Fatal(err)
		}
		if !has() {
			t.Fatalf("round %d, up: no sale_key", round)
		}
		if _, err := p.DownTo(ctx, 505); err != nil {
			t.Fatal(err)
		}
		if has() {
			t.Fatalf("round %d, down: sale_key kept", round)
		}
	}
}
