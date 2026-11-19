package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00475 (finding people) goes up, down and up again: its Down drops the
// person's settings, contacts, link, presence, counts and verdicts, the
// six definers, and what it granted taskiem_dispatch.
func TestPeopleFindingMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 475); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, q := range []string{
		`INSERT INTO users (id, email, name) VALUES ('00000000-0000-0000-0000-0000000000c1', 'tolu@x.test', 'Tolu'), ('00000000-0000-0000-0000-0000000000c2', 'ada@x.test', 'Ada')`,
		`INSERT INTO person_privacy (person_id, show_number) VALUES ('00000000-0000-0000-0000-0000000000c1', true)`,
		`INSERT INTO person_contacts (person_id, contact_id, source) VALUES ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000c2', 'number')`,
		`INSERT INTO person_presence (person_id) VALUES ('00000000-0000-0000-0000-0000000000c1')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.DownTo(ctx, 470); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname LIKE 'taskiem_people_%' OR proname = 'taskiem_person_link_find'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("definers after Down: %d %v", n, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE tablename IN ('person_privacy', 'person_contacts', 'person_links', 'person_presence', 'people_find_counts', 'people_screen_verdicts')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("tables after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 475); err != nil {
		t.Fatal(err)
	}
}
