package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00450 (docs/spec/taskiem-for-everyone.md, review fixes): a tenant made
// with "Just workflows for now" (every business area off, Schools off)
// becomes a workflows workspace; one with an area on, or with no area
// rows, stays a business. Down and up again work.
func TestStage1FixesMigrationKinds(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 449); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const (
		off  = "00000000-0000-0000-0000-0000000000a1"
		on   = "00000000-0000-0000-0000-0000000000a2"
		none = "00000000-0000-0000-0000-0000000000a3"
	)
	for _, id := range []string{off, on, none} {
		exec(`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'T', gen_random_uuid())`, id)
	}
	for _, a := range []string{"storefront", "orders", "stock", "money", "links"} {
		exec(`INSERT INTO business_areas (tenant_id, area, enabled, updated_by) VALUES ($1, $2, false, 'signup')`, off, a)
		exec(`INSERT INTO business_areas (tenant_id, area, enabled, updated_by) VALUES ($1, $2, $3, 'signup')`, on, a, a == "money")
	}
	kind := func(id string) string {
		t.Helper()
		var k string
		if err := conn.QueryRow(ctx, `SELECT kind FROM tenants WHERE id = $1`, id).Scan(&k); err != nil {
			t.Fatal(err)
		}
		return k
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 450); err != nil {
			t.Fatal(err)
		}
		if kind(off) != "workflows" || kind(on) != "business" || kind(none) != "business" {
			t.Fatalf("round %d: kinds %s %s %s", round, kind(off), kind(on), kind(none))
		}
		if _, err := p.DownTo(ctx, 449); err != nil {
			t.Fatal(err)
		}
	}
}
