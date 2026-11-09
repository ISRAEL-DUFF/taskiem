package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// 00408 (riders phase 1) goes up and down: its Down takes the riders'
// delivery orders with their link rows, in the order the foreign key
// allows, and puts the reference check back.
func TestRidersLinkedMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 408); err != nil {
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
	// A business, a rider's business, their link, and a delivery order in
	// the rider's business.
	exec(`INSERT INTO tenants (id, name, plan_id) VALUES ('00000000-0000-0000-0000-0000000000a1', 'Ada Stores', gen_random_uuid()),
		('00000000-0000-0000-0000-0000000000a2', 'Musa Rides', gen_random_uuid())`)
	exec(`INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at) VALUES ('00000000-0000-0000-0000-0000000000e1',
		'00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000a2', 'active', '00000000-0000-0000-0000-0000000000a1', 'user:ada', 'search', 'supplier', now() + interval '30 days')`)
	exec(`INSERT INTO storefront_orders (id, tenant_id, kind, reference, status_hash, status, customer, subject_id, payment)
		VALUES ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000a2', 'trade', 'AD-1-D', '\x00', 'received', '{}', 'phone:x', 'none')`)
	exec(`INSERT INTO link_orders (order_id, tenant_id, link_id, buyer_tenant, buyer_name, source_id, purchase_reference, lines, delivery_id)
		VALUES ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000a2', '00000000-0000-0000-0000-0000000000e1',
		'00000000-0000-0000-0000-0000000000a1', 'Ada Stores', '00000000-0000-0000-0000-0000000000d1', 'AD-1', '[{"item":"Delivery","quantity":1,"price_kobo":1000}]',
		'00000000-0000-0000-0000-0000000000d1')`)
	if _, err := p.DownTo(ctx, 407); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM storefront_orders WHERE kind = 'trade'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the rider's order after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 408); err != nil {
		t.Fatal(err)
	}
}
