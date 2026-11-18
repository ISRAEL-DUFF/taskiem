package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00484 (safety in groups) goes up, down and up again: up, the ledger,
// the group's standing, the group columns of reports, held messages,
// moderators' actions and lawful requests, and the new definers are there,
// owned by taskiem_safety; a person's report and action made before stay
// as they were; down, all is as 00483 left it (a group's actions go).
func TestPeopleGroupSafetyMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 483); err != nil {
		t.Fatal(err)
	}
	const (
		ada    = "00000000-0000-0000-0000-0000000000e1"
		bola   = "00000000-0000-0000-0000-0000000000e2"
		report = "00000000-0000-0000-0000-0000000000a1"
		action = "00000000-0000-0000-0000-0000000000a2"
	)
	for _, q := range []string{
		`INSERT INTO users (id, email, name) VALUES ('` + ada + `', 'ada@x.test', 'Ada'), ('` + bola + `', 'bola@x.test', 'Bola')`,
		`INSERT INTO people_reports (id, person_id, reported_id, reason, consent, messages) VALUES ('` + report + `', '` + ada + `', '` + bola + `', 'spam', true, '[]')`,
		`INSERT INTO people_moderation (id, person_id, action, reason, operator) VALUES ('` + action + `', '` + bola + `', 'warn', 'Rude', 'operator:x')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	type st struct {
		tables, columns, definers, safetyOwned int
		kept                                   int
	}
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT
			(SELECT count(*)::int FROM pg_class WHERE relname IN ('group_ledger', 'group_standing') AND relkind = 'r'),
			(SELECT count(*)::int FROM information_schema.columns WHERE (table_name, column_name) IN (('people_reports', 'group_id'), ('people_reports', 'group_sealed'),
			   ('people_held', 'group_id'), ('people_held', 'mentions'), ('people_moderation', 'group_id'), ('lawful_requests', 'scope_groups'),
			   ('group_messages', 'screen'), ('people_screen_verdicts', 'group_id'))),
			(SELECT count(*)::int FROM pg_proc WHERE proname IN ('taskiem_ops_group_moderate', 'taskiem_group_closures_due', 'taskiem_group_close_carry',
			   'taskiem_ops_group', 'taskiem_ops_group_members', 'taskiem_ops_group_actions', 'taskiem_ops_group_reports', 'taskiem_group_standing_sync')),
			(SELECT count(*)::int FROM pg_proc WHERE proname IN ('taskiem_ops_people_lift', 'taskiem_ops_people_appeal_decide', 'taskiem_ops_people_reports',
			   'taskiem_ops_people_report', 'taskiem_ops_people_held', 'taskiem_ops_people_held_one', 'taskiem_ops_people_verdicts', 'taskiem_ops_people_appeals',
			   'taskiem_ops_lawful_record', 'taskiem_ops_lawful_requests') AND proowner = 'taskiem_safety'::regrole),
			(SELECT count(*)::int FROM people_reports WHERE id = '`+report+`') + (SELECT count(*)::int FROM people_moderation WHERE id = '`+action+`')`).
			Scan(&s.tables, &s.columns, &s.definers, &s.safetyOwned, &s.kept); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 484); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.tables != 2 || s.columns != 8 || s.definers != 8 || s.safetyOwned != 10 || s.kept != 2 {
			t.Fatalf("up: %+v", s)
		}
		if _, err := p.DownTo(ctx, 483); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.tables != 0 || s.columns != 0 || s.definers != 0 || s.safetyOwned != 10 || s.kept != 2 {
			t.Fatalf("down: %+v", s)
		}
	}
	if _, err := p.UpTo(ctx, 484); err != nil {
		t.Fatal(err)
	}
}
