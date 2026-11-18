package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00481 (the second review's fixes) goes up, down and up again: up, the
// safety tables are taskiem_safety's and not taskiem_dispatch's, the
// moderators' definers are owned by taskiem_safety, and the answering
// reach exists; down, all is as 00480 left it.
func TestPeopleReviewFixes2MigrationUpDownUp(t *testing.T) {
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
	type st struct {
		dispatchReads, safetyOwns, reachTry bool
	}
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT has_column_privilege('taskiem_dispatch', 'people_appeals', 'text', 'SELECT'),
			EXISTS (SELECT 1 FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner WHERE p.proname = 'taskiem_ops_people_report' AND r.rolname = 'taskiem_safety'),
			EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'taskiem_people_reach_try')`).Scan(&s.dispatchReads, &s.safetyOwns, &s.reachTry); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if _, err := p.UpTo(ctx, 481); err != nil {
		t.Fatal(err)
	}
	if s := state(); s.dispatchReads || !s.safetyOwns || !s.reachTry {
		t.Fatalf("up: %+v", s)
	}
	if _, err := p.DownTo(ctx, 480); err != nil {
		t.Fatal(err)
	}
	if s := state(); !s.dispatchReads || s.safetyOwns || s.reachTry {
		t.Fatalf("down: %+v", s)
	}
	if _, err := p.UpTo(ctx, 481); err != nil {
		t.Fatal(err)
	}
	if s := state(); s.dispatchReads || !s.safetyOwns || !s.reachTry {
		t.Fatalf("up again: %+v", s)
	}
}
