package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00494 (saved addresses to saved things) moves each address into the
// person's own place objects under the same id: its label the title, a
// name made from it (taken ones get -2), the default kept; it goes down
// (the addresses back) and up again.
func TestSavedAddressesMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 493); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	const tolu = "00000000-0000-0000-0000-0000000000b1"
	for _, q := range []string{
		`INSERT INTO users (id, email, name) VALUES ('` + tolu + `', 'tolu@x.test', 'Tolu')`,
		// A saved thing already called @home: the address takes @home-2.
		`INSERT INTO saved_objects (id, person_id, name, title, kind, details, sealed, hint, check_state)
			VALUES ('00000000-0000-0000-0000-0000000000c1', '` + tolu + `', 'home', 'Home text', 'text', '{"body": "x"}', '{}', '1 line of text', 'none')`,
		`INSERT INTO person_addresses (id, person_id, label, line, area, phone, is_default)
			VALUES ('00000000-0000-0000-0000-0000000000a1', '` + tolu + `', 'Home', '12 Allen Avenue', 'Ikeja', '+2348031120099', true)`,
		`INSERT INTO person_addresses (id, person_id, label, line, landmark)
			VALUES ('00000000-0000-0000-0000-0000000000a2', '` + tolu + `', 'Mum''s place', '4 Bode Thomas Street', 'opposite the bakery')`,
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.UpTo(ctx, 494); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, `SELECT id::text, name, title, details::text, hint, default_place FROM saved_objects WHERE kind = 'place' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type place struct {
		id, name, title, details, hint string
		def                            bool
	}
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (place, error) {
		var x place
		err := r.Scan(&x.id, &x.name, &x.title, &x.details, &x.hint, &x.def)
		return x, err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []place{
		{"00000000-0000-0000-0000-0000000000a1", "home-2", "Home", `{"area": "Ikeja", "line": "12 Allen Avenue", "phone": "+2348031120099"}`, "Ikeja", true},
		{"00000000-0000-0000-0000-0000000000a2", "mums-place", "Mum's place", `{"line": "4 Bode Thomas Street", "landmark": "opposite the bakery"}`, "4 Bode Thomas Street", false},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("moved: %+v", got)
	}
	if _, err := p.DownTo(ctx, 493); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM person_addresses WHERE is_default AND label = 'Home'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("back after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 494); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM saved_objects WHERE kind = 'place' AND default_place`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("up again: %d %v", n, err)
	}
}
