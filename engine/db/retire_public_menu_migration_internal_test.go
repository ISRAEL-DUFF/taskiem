package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migration 00640 retires the separate public menu (build plan H6): its
// list and its switch go, whatever they held (nothing is in production);
// down brings both back empty, and 00569's down (the commands it gave the
// list) still runs under it; then up again.
func TestRetirePublicMenuMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 639); err != nil {
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
	a := "00000000-0000-0000-0000-0000000000a1"
	exec(`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Acme', gen_random_uuid())`, a)
	exec(`INSERT INTO whatsapp_public_workflows (tenant_id, workflow_id, label, created_by, command) VALUES ($1, gen_random_uuid(), 'Track', 'u', 'track')`, a)
	exists := func(q string) bool {
		t.Helper()
		var ok bool
		if err := conn.QueryRow(ctx, q).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	const table = `SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = 'whatsapp_public_workflows')`
	const column = `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'whatsapp_numbers' AND column_name = 'public_menu')`
	if _, err := p.UpTo(ctx, 640); err != nil {
		t.Fatal(err)
	}
	if exists(table) || exists(column) {
		t.Fatal("the public menu's list or switch is still there")
	}
	if _, err := p.DownTo(ctx, 639); err != nil {
		t.Fatal(err)
	}
	if !exists(table) || !exists(column) {
		t.Fatal("down did not bring the list and the switch back")
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM whatsapp_public_workflows`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("entries after down: %d %v", n, err)
	}
	// 00569's down runs on what 00640's down made.
	if _, err := p.DownTo(ctx, 567); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 640); err != nil {
		t.Fatal(err)
	}
}
