package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00483 (the groups review's fixes) goes up, down and up again: up, someone
// removed before is kept as removed (group_removed, from their membership),
// group_members has marks_at, the link's preview no longer says whether
// joining waits, and the new definers are there; down, all is as 00482
// left it.
func TestPeopleGroupsReviewMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 482); err != nil {
		t.Fatal(err)
	}
	const (
		ada   = "00000000-0000-0000-0000-0000000000e1"
		bola  = "00000000-0000-0000-0000-0000000000e2"
		group = "00000000-0000-0000-0000-0000000000f1"
	)
	for _, q := range []string{
		`INSERT INTO users (id, email, name) VALUES ('` + ada + `', 'ada@x.test', 'Ada'), ('` + bola + `', 'bola@x.test', 'Bola')`,
		`INSERT INTO people_groups (id, sealed) VALUES ('` + group + `', '\x00')`,
		`INSERT INTO group_members (group_id, person_id, role, how, visible) VALUES ('` + group + `', '` + ada + `', 'admin', 'created', tstzmultirange(tstzrange(now(), NULL)))`,
		`INSERT INTO group_members (group_id, person_id, how, left_at, left_how, removed_by, visible)
		 VALUES ('` + group + `', '` + bola + `', 'added', now(), 'removed', '` + ada + `', '{}')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	type st struct {
		removed     int
		marks, find bool
		approve     bool
		definers    int
	}
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT to_regclass('group_removed') IS NOT NULL,
			EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'group_members' AND column_name = 'marks_at'),
			EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'taskiem_group_link_find' AND 'approve_joins' = ANY (proargnames)),
			(SELECT count(*)::int FROM pg_proc WHERE proname IN ('taskiem_group_forget', 'taskiem_group_push'))`).Scan(&s.find, &s.marks, &s.approve, &s.definers); err != nil {
			t.Fatal(err)
		}
		if s.find {
			if err := conn.QueryRow(ctx, `SELECT count(*) FROM group_removed WHERE group_id = '`+group+`' AND person_id = '`+bola+`' AND removed_by = '`+ada+`'`).Scan(&s.removed); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 483); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.removed != 1 || !s.marks || !s.find || s.approve || s.definers != 2 {
			t.Fatalf("up: %+v", s)
		}
		if _, err := p.DownTo(ctx, 482); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.find || s.marks || !s.approve || s.definers != 0 {
			t.Fatalf("down: %+v", s)
		}
	}
	if _, err := p.UpTo(ctx, 483); err != nil {
		t.Fatal(err)
	}
}
