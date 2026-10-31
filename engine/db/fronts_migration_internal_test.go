package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// The fronts migration (00279; docs/spec/fronts.md §9) up, down and up
// again over a storefront with actions and submissions: up gives the
// storefront its first front, the actions ids on it, and every submission
// its action and front, a kind with submissions and no action included;
// down puts the old key back with every row kept; up again repeats it.
func TestFrontsMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 278); err != nil {
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
	exec(`INSERT INTO tenants (id, name, plan_id) VALUES ('00000000-0000-0000-0000-0000000000a1', 'Ada Kitchen', gen_random_uuid())`)
	exec(`INSERT INTO storefronts (id, tenant_id, slug, draft, created_by) VALUES ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1',
		'ada-kitchen', '{"name": "Ada Kitchen"}', 'user:ada')`)
	exec(`INSERT INTO storefront_actions (storefront_id, kind, tenant_id, enabled, label, payment) VALUES
		('00000000-0000-0000-0000-0000000000b1', 'order', '00000000-0000-0000-0000-0000000000a1', true, 'Order now', 'after_accept'),
		('00000000-0000-0000-0000-0000000000b1', 'enquiry', '00000000-0000-0000-0000-0000000000a1', true, 'Ask', 'none')`)
	order := func(id, kind, ref string) {
		t.Helper()
		exec(`INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, payment)
			VALUES ($1, '00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000b1', $2, $3, '\x00', 'received', '{}', 'phone:x', 'none')`, id, kind, ref)
	}
	order("00000000-0000-0000-0000-0000000000c1", "order", "ADA-1")
	order("00000000-0000-0000-0000-0000000000c2", "callback", "ADA-2") // a kind with no action row

	check := func(when string) {
		t.Helper()
		var fronts, actions, unlinked, callbacks int
		var home string
		if err := conn.QueryRow(ctx, `SELECT count(*), max(name) FROM storefront_fronts WHERE slug = ''`).Scan(&fronts, &home); err != nil || fronts != 1 || home != "Ada Kitchen" {
			t.Fatalf("%s: first front %d %q %v", when, fronts, home, err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM storefront_actions a JOIN storefront_fronts f ON f.id = a.front_id AND f.slug = ''
			WHERE a.builtin AND a.slug = a.kind`).Scan(&actions); err != nil || actions != 3 {
			t.Fatalf("%s: built-in actions on the first front: %d %v", when, actions, err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM storefront_orders o LEFT JOIN storefront_actions a ON a.id = o.action_id
			WHERE a.id IS NULL OR a.kind <> o.kind OR o.front_id <> a.front_id`).Scan(&unlinked); err != nil || unlinked != 0 {
			t.Fatalf("%s: submissions without their action: %d %v", when, unlinked, err)
		}
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM storefront_actions WHERE kind = 'callback' AND NOT enabled`).Scan(&callbacks); err != nil || callbacks != 1 {
			t.Fatalf("%s: the made callback action: %d %v", when, callbacks, err)
		}
	}
	if _, err := p.UpTo(ctx, 279); err != nil {
		t.Fatal(err)
	}
	check("up")
	// A row an older binary writes is filled in.
	order("00000000-0000-0000-0000-0000000000c3", "order", "ADA-3")
	var filled bool
	if err := conn.QueryRow(ctx, `SELECT action_id IS NOT NULL AND front_id IS NOT NULL FROM storefront_orders WHERE reference = 'ADA-3'`).Scan(&filled); err != nil || !filled {
		t.Fatalf("an older binary's order: %v %v", filled, err)
	}

	// 00280 moves the definers to action ids; its Down puts them back.
	if _, err := p.UpTo(ctx, 280); err != nil {
		t.Fatal(err)
	}
	var claimCols int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.parameters WHERE specific_name LIKE 'taskiem_storefront_inbox_claim%' AND parameter_mode = 'OUT'`).Scan(&claimCols); err != nil || claimCols != 9 {
		t.Fatalf("the claim's columns at 280: %d %v", claimCols, err)
	}
	if _, err := p.DownTo(ctx, 278); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM storefront_orders`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("down kept %d orders: %v", n, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.parameters WHERE specific_name LIKE 'taskiem_storefront_inbox_claim%' AND parameter_mode = 'OUT'`).Scan(&claimCols); err != nil || claimCols != 8 {
		t.Fatalf("the claim's columns after down: %d %v", claimCols, err)
	}
	var key string
	if err := conn.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'storefront_actions_pkey'`).Scan(&key); err != nil ||
		key != "PRIMARY KEY (storefront_id, kind)" {
		t.Fatalf("down's key: %q %v", key, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	check("up again")
}
