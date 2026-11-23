package db

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Migration 00569 carries each public menu's entries over as customers'
// tools: a command made from each label, unique in its business (another
// business may have the same), starting with a letter, at most 24
// characters, never one of Taskiem's own words or the routes' "buttons";
// then down and up again.
func TestCustomersToolsMigration(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 567); err != nil {
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
	a, b := "00000000-0000-0000-0000-0000000000a1", "00000000-0000-0000-0000-0000000000a2"
	exec(`INSERT INTO tenants (id, name, plan_id) VALUES ($1, 'Acme', gen_random_uuid()), ($2, 'Beta', gen_random_uuid())`, a, b)
	entry := func(tenant, wf, label string, at int) {
		t.Helper()
		exec(`INSERT INTO whatsapp_public_workflows (tenant_id, workflow_id, label, created_by, created_at) VALUES ($1, $2, $3, 'u', now() + make_interval(secs => $4))`, tenant, wf, label, at)
	}
	entry(a, "00000000-0000-0000-0000-0000000000b1", "Pay a supplier", 1)
	entry(a, "00000000-0000-0000-0000-0000000000b2", "Pay a supplier!", 2)
	entry(a, "00000000-0000-0000-0000-0000000000b3", "24/7 help", 3)
	entry(a, "00000000-0000-0000-0000-0000000000b4", "Ọ̀rọ̀", 4)
	entry(a, "00000000-0000-0000-0000-0000000000b5", "Check where my parcel is", 5)
	entry(b, "00000000-0000-0000-0000-0000000000b6", "Pay a supplier", 1)
	entry(b, "00000000-0000-0000-0000-0000000000b7", "Order", 2)
	entry(b, "00000000-0000-0000-0000-0000000000b8", "Buttons", 3)
	if _, err := p.UpTo(ctx, 569); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	rows, err := conn.Query(ctx, `SELECT tenant_id::text || '/' || label, command FROM whatsapp_public_workflows`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k, c string
		if err := rows.Scan(&k, &c); err != nil {
			t.Fatal(err)
		}
		got[k] = c
	}
	rows.Close()
	want := map[string]string{
		a + "/Pay a supplier":           "pay-a-supplier",
		a + "/Pay a supplier!":          "pay-a-supplier-2",
		a + "/24/7 help":                "menu-24-7-help",
		a + "/Ọ̀rọ̀":                    "item",
		a + "/Check where my parcel is": "check-where-my-parcel",
		b + "/Pay a supplier":           "pay-a-supplier",
		b + "/Order":                    "order-2",
		b + "/Buttons":                  "buttons-2",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	// A command is one entry's in its business.
	if _, err := conn.Exec(ctx, `UPDATE whatsapp_public_workflows SET command = 'pay-a-supplier' WHERE label = 'Pay a supplier!'`); err == nil {
		t.Error("two entries of a business share a command")
	}
	if _, err := p.DownTo(ctx, 567); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_proc WHERE proname = 'taskiem_customer_tool_tenants'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after Down: %d %v", n, err)
	}
	if _, err := p.UpTo(ctx, 569); err != nil {
		t.Fatal(err)
	}
}
