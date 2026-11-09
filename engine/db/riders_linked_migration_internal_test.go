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

// 00409 (riders phase 2) goes up and down: the profile's delivery fields,
// the two phone definers, and Explore's definer back to 00401's shape.
func TestRiderProfileMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 409); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, `INSERT INTO tenants (id, name, plan_id) VALUES ('00000000-0000-0000-0000-0000000000a2', 'Musa Rides', gen_random_uuid())`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO business_profile (tenant_id, delivers, vehicle, delivery_areas, updated_by) VALUES ('00000000-0000-0000-0000-0000000000a2', true, 'bike', '{Lekki}', 'test')`); err != nil {
		t.Fatal(err)
	}
	var cols int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('taskiem_link_phone_linked', 'taskiem_link_phone_is_business')`).Scan(&cols); err != nil || cols != 2 {
		t.Fatalf("the phone definers: %d %v", cols, err)
	}
	if _, err := p.DownTo(ctx, 408); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'business_profile' AND column_name IN ('delivers', 'vehicle', 'delivery_areas')`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("the columns after Down: %d %v", cols, err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'taskiem_link_explore'`).Scan(&cols); err != nil || cols != 1 {
		t.Fatalf("explore after Down: %d %v", cols, err)
	}
	if _, err := p.UpTo(ctx, 409); err != nil {
		t.Fatal(err)
	}
}

// 00410 (riders phase 3) goes up and down: the availability definers.
func TestRiderAvailabilityMigrationUpDownUp(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 410); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname IN ('taskiem_rider_own_hashes', 'taskiem_rider_availability_say', 'taskiem_rider_availability', 'taskiem_link_availability')`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("the availability definers: %d %v", n, err)
	}
	if _, err := p.DownTo(ctx, 409); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname LIKE 'taskiem_rider_availability%' OR proname = 'taskiem_link_availability'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 410); err != nil {
		t.Fatal(err)
	}
}
