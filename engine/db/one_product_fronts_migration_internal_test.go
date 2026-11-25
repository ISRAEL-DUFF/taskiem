package db

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00820 (forms that sell L2: a front may be of the One product kind) goes
// up, down and up again.
func TestOneProductFrontsMigrationUpDownUp(t *testing.T) {
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
	takes := func() bool {
		t.Helper()
		var def string
		if err := conn.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'storefront_fronts_kind_check'`).Scan(&def); err != nil {
			t.Fatal(err)
		}
		return strings.Contains(def, "one-product")
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for round := 0; round < 2; round++ {
		if _, err := p.UpTo(ctx, 820); err != nil {
			t.Fatal(err)
		}
		if !takes() {
			t.Fatalf("round %d, up: a One product front refused", round)
		}
		if round == 0 {
			exec(`INSERT INTO tenants (id, name, plan_id) VALUES ('00000000-0000-0000-0000-0000000000a1', 'Ada Stores', gen_random_uuid())`)
			exec(`INSERT INTO storefronts (id, tenant_id, slug, draft, created_by) VALUES ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1',
				'ada-stores', '{"name": "Ada Stores"}', 'user:ada')`)
		}
		// A One product front made while it is up.
		slug := fmt.Sprintf("ear-%d", round)
		exec(`INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, position, enabled, options) VALUES (gen_random_uuid(),
			'00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000b1', $1, 'one-product', 'Smart Ear Cleaner', $2, true,
			'{"promise": "Pay on delivery nationwide", "stock_line": true}')`, slug, round+1)
		if _, err := p.DownTo(ctx, 819); err != nil {
			t.Fatal(err)
		}
		if takes() {
			t.Fatalf("round %d, down: a One product front still taken", round)
		}
		// Down, it is an ordinary front of one list, its settings left out.
		var kind, options string
		if err := conn.QueryRow(ctx, `SELECT kind, options::text FROM storefront_fronts WHERE slug = $1`, slug).Scan(&kind, &options); err != nil {
			t.Fatal(err)
		}
		if kind != "custom" || options != "{}" {
			t.Fatalf("round %d, down: the One product front is %s %s", round, kind, options)
		}
	}
}
