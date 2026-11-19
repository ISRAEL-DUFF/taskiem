package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00485 (the review's fixes to safety in groups) goes up, down and up
// again: up, a member's report from a group made before is marked
// group_member and a whole group's is not, people_held has client_id,
// taskiem_people_held_again is there owned by taskiem_safety, and a
// group message's screen is warn only; down, all is as 00484 left it.
func TestPeopleGroupSafetyReviewMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 484); err != nil {
		t.Fatal(err)
	}
	const (
		ada    = "00000000-0000-0000-0000-0000000000e1"
		bola   = "00000000-0000-0000-0000-0000000000e2"
		group  = "00000000-0000-0000-0000-0000000000c1"
		member = "00000000-0000-0000-0000-0000000000a1"
		whole  = "00000000-0000-0000-0000-0000000000a2"
	)
	for _, q := range []string{
		`INSERT INTO users (id, email, name) VALUES ('` + ada + `', 'ada@x.test', 'Ada'), ('` + bola + `', 'bola@x.test', 'Bola')`,
		`INSERT INTO people_reports (id, person_id, reported_id, reason, consent, messages, group_id) VALUES ('` + member + `', '` + ada + `', '` + bola + `', 'spam', true, '[]', '` + group + `'),
		   ('` + whole + `', '` + ada + `', NULL, 'spam', true, '[]', '` + group + `')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	type st struct {
		columns, definer, members int
		screen                    string
	}
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM information_schema.columns WHERE (table_name, column_name) IN (('people_reports', 'group_member'), ('people_held', 'client_id'))),
			(SELECT count(*)::int FROM pg_proc WHERE proname = 'taskiem_people_held_again' AND proowner = 'taskiem_safety'::regrole),
			(SELECT count(*)::int FROM people_reports r WHERE COALESCE((to_jsonb(r)->>'group_member')::boolean, false)),
			(SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'group_messages_screen_check')`).
			Scan(&s.columns, &s.definer, &s.members, &s.screen); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 485); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.columns != 2 || s.definer != 1 || s.members != 1 || s.screen != "CHECK ((screen = 'warn'::text))" {
			t.Fatalf("up: %+v", s)
		}
		if _, err := p.DownTo(ctx, 484); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.columns != 0 || s.definer != 0 || s.members != 0 || s.screen != "CHECK ((screen = ANY (ARRAY['warn'::text, 'transfer'::text])))" {
			t.Fatalf("down: %+v", s)
		}
	}
	if _, err := p.UpTo(ctx, 485); err != nil {
		t.Fatal(err)
	}
}
