package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00478 (a suspended account's card) goes up, down and up again: up,
// taskiem_people_cards answers deleted apart from active; down, it is as
// 00476 left it; and taskiem_app may execute it either way.
func TestPeopleSuspendedCardMigrationUpDownUp(t *testing.T) {
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
	result := func() string {
		t.Helper()
		var r string
		var granted bool
		if err := conn.QueryRow(ctx, `SELECT pg_get_function_result(p.oid), has_function_privilege('taskiem_app', p.oid, 'EXECUTE')
			FROM pg_proc p WHERE p.proname = 'taskiem_people_cards'`).Scan(&r, &granted); err != nil {
			t.Fatal(err)
		}
		if !granted {
			t.Fatal("taskiem_app may not execute taskiem_people_cards")
		}
		return r
	}
	if _, err := p.UpTo(ctx, 478); err != nil {
		t.Fatal(err)
	}
	if r := result(); !strings.Contains(r, "active boolean, deleted boolean, name text") {
		t.Fatalf("up: %s", r)
	}
	if _, err := p.DownTo(ctx, 477); err != nil {
		t.Fatal(err)
	}
	if r := result(); strings.Contains(r, "deleted boolean") {
		t.Fatalf("down: %s", r)
	}
	if _, err := p.UpTo(ctx, 478); err != nil {
		t.Fatal(err)
	}
	if r := result(); !strings.Contains(r, "deleted boolean") {
		t.Fatalf("up again: %s", r)
	}
}
