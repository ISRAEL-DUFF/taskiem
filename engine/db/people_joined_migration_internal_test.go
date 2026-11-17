package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00476 (the two streams of stage 5a joined) goes up, down and up again:
// up, the one table of names is person_profiles (person_names, its
// trigger and function gone) and taskiem_people_between exists; down,
// person_names is back, filled from users and kept by its trigger.
func TestPeopleJoinedMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 476); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `INSERT INTO users (id, email, name) VALUES ('00000000-0000-0000-0000-0000000000d1', 'tolu@x.test', 'Tolu')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE tablename = 'person_names'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("person_names after Up: %d %v", n, err)
	}
	var name string
	if err := conn.QueryRow(ctx, `SELECT name FROM person_profiles WHERE person_id = '00000000-0000-0000-0000-0000000000d1'`).Scan(&name); err != nil || name != "Tolu" {
		t.Fatalf("person_profiles: %q %v", name, err)
	}
	if _, err := p.DownTo(ctx, 475); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'taskiem_people_between'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("taskiem_people_between after Down: %d %v", n, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE users SET name = 'Tolu A' WHERE id = '00000000-0000-0000-0000-0000000000d1'`); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT name FROM person_names WHERE person_id = '00000000-0000-0000-0000-0000000000d1'`).Scan(&name); err != nil || name != "Tolu A" {
		t.Fatalf("person_names after Down: %q %v", name, err)
	}
	if _, err := p.UpTo(ctx, 476); err != nil {
		t.Fatal(err)
	}
}
