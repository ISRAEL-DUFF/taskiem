package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00480 (safety and moderation) goes up, down and up again: up, the
// moderators' tables and functions exist and a report outlives its
// reporter (person_id nullable); down, they are gone and people_reports is
// as 00471 made it.
func TestPeopleSafetyMigrationUpDownUp(t *testing.T) {
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
	state := func() (tables int, nullable bool, granted bool) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public'
			AND tablename IN ('people_report_files', 'people_held', 'people_moderation', 'people_appeals', 'lawful_requests')`).Scan(&tables); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT is_nullable = 'YES' FROM information_schema.columns WHERE table_name = 'people_reports' AND column_name = 'person_id'`).
			Scan(&nullable); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT COALESCE(bool_or(has_function_privilege('taskiem_app', p.oid, 'EXECUTE')), false) FROM pg_proc p
			WHERE p.proname = 'taskiem_lawful_request_counts'`).Scan(&granted); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := p.UpTo(ctx, 480); err != nil {
		t.Fatal(err)
	}
	if n, null, g := state(); n != 5 || !null || !g {
		t.Fatalf("up: %d tables, nullable %v, granted %v", n, null, g)
	}
	if _, err := p.DownTo(ctx, 478); err != nil {
		t.Fatal(err)
	}
	if n, null, g := state(); n != 0 || null || g {
		t.Fatalf("down: %d tables, nullable %v, granted %v", n, null, g)
	}
	if _, err := p.UpTo(ctx, 480); err != nil {
		t.Fatal(err)
	}
	if n, null, _ := state(); n != 5 || !null {
		t.Fatalf("up again: %d tables, nullable %v", n, null)
	}
}
