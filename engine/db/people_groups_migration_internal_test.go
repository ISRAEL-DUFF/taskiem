package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00482 (groups) goes up, down and up again: up, the group tables exist
// with row-level security, the new privacy setting and push kinds are
// there, and a group can be made through its definer; down, all is as
// 00481 left it (a person's settings kept).
func TestPeopleGroupsMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 481); err != nil {
		t.Fatal(err)
	}
	const ada = "00000000-0000-0000-0000-0000000000e1"
	for _, q := range []string{
		`INSERT INTO users (id, email, name) VALUES ('` + ada + `', 'ada@x.test', 'Ada')`,
		`INSERT INTO person_privacy (person_id, message) VALUES ('` + ada + `', 'contacts')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	type st struct {
		tables, rls int
		setting     bool
	}
	state := func() (s st) {
		t.Helper()
		if err := conn.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE relrowsecurity AND relforcerowsecurity) FROM pg_class
			WHERE relkind = 'r' AND (relname LIKE 'group\_%' OR relname = 'people_groups')`).Scan(&s.tables, &s.rls); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'person_privacy' AND column_name = 'groups_add')`).Scan(&s.setting); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 482); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.tables != 14 || s.rls != 14 || !s.setting {
			t.Fatalf("up: %+v", s)
		}
		var add string
		if err := conn.QueryRow(ctx, `SELECT groups_add FROM person_privacy WHERE person_id = '`+ada+`'`).Scan(&add); err != nil || add != "contacts" {
			t.Fatalf("the default: %q %v", add, err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO push_outbox (id, user_id, kind, title, body, link, tag) VALUES (gen_random_uuid(), '`+ada+`', 'group', 'x', 'y', '/you', 'g')`); err != nil {
			t.Fatalf("a group push: %v", err)
		}
		if _, err := p.DownTo(ctx, 481); err != nil {
			t.Fatal(err)
		}
		if s := state(); s.tables != 0 || s.setting {
			t.Fatalf("down: %+v", s)
		}
		var message string
		if err := conn.QueryRow(ctx, `SELECT message FROM person_privacy WHERE person_id = '`+ada+`'`).Scan(&message); err != nil || message != "contacts" {
			t.Fatalf("settings kept: %q %v", message, err)
		}
	}
	if _, err := p.UpTo(ctx, 482); err != nil {
		t.Fatal(err)
	}
}
