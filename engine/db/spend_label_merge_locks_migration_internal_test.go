package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00505 (a label merge is one step under two at once) goes up, down and
// up again: its Up makes taskiem_spend_label_merge lock the label merged
// into whatever it is, its Down puts back 00503's, locking only a label
// merged already.
func TestSpendLabelMergeLocksMigrationUpDownUp(t *testing.T) {
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
	locksAlways := func() bool {
		t.Helper()
		var src string
		if err := conn.QueryRow(ctx, `SELECT prosrc FROM pg_proc WHERE proname = 'taskiem_spend_label_merge'`).Scan(&src); err != nil {
			t.Fatal(err)
		}
		return strings.Contains(src, "INTO target_merged") && !strings.Contains(src, "merged_into IS NOT NULL FOR SHARE")
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 505); err != nil {
			t.Fatal(err)
		}
		if !locksAlways() {
			t.Fatalf("round %d, up: the old function", round)
		}
		if _, err := p.DownTo(ctx, 504); err != nil {
			t.Fatal(err)
		}
		if locksAlways() {
			t.Fatalf("round %d, down: the new function", round)
		}
	}
}
