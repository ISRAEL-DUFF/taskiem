package db_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// taskiem_product_left (00502, what a business buys §4.1) answers how many
// whole sales each counted product has left (the count over the uses,
// rounded down, never below 0) and nothing else: the public role reads it
// for a business with a page online, the app role for the business in
// scope or one actively linked to it. Neither reads anything else about a
// stock item: no table of stock is open to the public role, and the app
// role sees no other business's.
func TestProductLeft(t *testing.T) {
	d := dbtest.New(t)
	shop := d.SeedTenant(t, nil)
	buyer := d.SeedTenant(t, nil)
	stranger := d.SeedTenant(t, nil)
	malt, short := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO stock_items (id, tenant_id, name, unit, cost_kobo, low_level, supplier_id, created_by) VALUES ($1, $2, 'Malt', 'bottle', 25000, 6, NULL, 'user:ada')`, []any{malt, shop.ID}},
		{`INSERT INTO stock_items (id, tenant_id, name, created_by) VALUES ($1, $2, 'Gone short', 'user:ada')`, []any{short, shop.ID}},
		{`INSERT INTO product_stock (tenant_id, product_id, stock_item_id, uses) VALUES ($1, 'pack', $2, 6), ($1, 'single', $2, 1), ($1, 'owed', $3, 2)`, []any{shop.ID, malt, short}},
	} {
		if _, err := d.Admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	// 11 bottles: one pack of 6 and 11 singles; a stock item gone below 0
	// by a sale beyond it has none left.
	if _, err := d.Admin.Exec(ctx, `INSERT INTO stock_movements (id, tenant_id, stock_item_id, item, reason, delta, count_before, count_after, change, actor, key)
		VALUES (gen_random_uuid(), $1, $2, 'Malt', 'received', 11, 0, 11, 11, 'user:ada', 'k1'),
		       (gen_random_uuid(), $1, $3, 'Gone short', 'adjustment', -3, 0, -3, -3, 'user:ada', 'k2')`, shop.ID, malt, short); err != nil {
		t.Fatal(err)
	}
	ids := []string{"pack", "single", "owed", "uncounted"}
	want := map[string]int64{"pack": 1, "single": 11, "owed": 0}
	left := func(rows map[string]int64) bool {
		if len(rows) != len(want) {
			return false
		}
		for k, v := range want {
			if rows[k] != v {
				return false
			}
		}
		return true
	}
	read := func(q func(string, ...any) (map[string]int64, error)) map[string]int64 {
		t.Helper()
		got, err := q(`SELECT product_id, sales_left FROM taskiem_product_left($1, $2)`, shop.ID, ids)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// The public role: nothing while the business has no page online.
	pub := d.PublicPool(t, 1)
	asPublic := func(q string, args ...any) (map[string]int64, error) {
		rows, err := pub.Query(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[string]int64{}
		for rows.Next() {
			var id string
			var n int64
			if err := rows.Scan(&id, &n); err != nil {
				return nil, err
			}
			out[id] = n
		}
		return out, rows.Err()
	}
	if got := read(asPublic); len(got) != 0 {
		t.Fatalf("no page online, the public role read %v", got)
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO storefronts (id, tenant_id, slug, status, created_by) VALUES ($1, $2, 'malt-house', 'published', 'user:ada')`,
		uuid.Must(uuid.NewV7()), shop.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(asPublic); !left(got) {
		t.Fatalf("the public role read %v, want %v", got, want)
	}
	// Nothing else about stock is the public role's to read.
	for _, q := range []string{
		"SELECT count(*) FROM stock_items", "SELECT count(*) FROM product_stock", "SELECT count(*) FROM stock_movements",
		"SELECT cost_kobo FROM stock_items", "SELECT uses FROM product_stock",
	} {
		if _, err := pub.Exec(ctx, q); sqlState(err) != "42501" {
			t.Errorf("the public role ran %q: %v", q, err)
		}
	}
	// It returns two columns: the product and how many sales are left.
	var cols int
	if err := d.Admin.QueryRow(ctx, `SELECT count(*) FROM pg_proc p, unnest(p.proargmodes) m WHERE p.proname = 'taskiem_product_left' AND m = 't'`).Scan(&cols); err != nil || cols != 2 {
		t.Fatalf("taskiem_product_left returns %d columns (err=%v)", cols, err)
	}

	// The app role: for the business in scope, or one linked to it.
	asApp := func(scope uuid.UUID) func(string, ...any) (map[string]int64, error) {
		return func(q string, args ...any) (map[string]int64, error) {
			out := map[string]int64{}
			err := scoped(t, d, []uuid.UUID{scope}, func(tx pgx.Tx) error {
				rows, err := tx.Query(ctx, q, args...)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var id string
					var n int64
					if err := rows.Scan(&id, &n); err != nil {
						return err
					}
					out[id] = n
				}
				return rows.Err()
			})
			return out, err
		}
	}
	if _, err := d.Admin.Exec(ctx, `UPDATE storefronts SET status = 'draft' WHERE tenant_id = $1`, shop.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(asApp(shop.ID)); !left(got) {
		t.Fatalf("the business in scope read %v", got)
	}
	if got := read(asApp(buyer.ID)); len(got) != 0 {
		t.Fatalf("a business not linked read %v", got)
	}
	lo, hi := shop.ID, buyer.ID
	if hi.String() < lo.String() {
		lo, hi = hi, lo
	}
	if _, err := d.Admin.Exec(ctx, `INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at, answered_at, answered_actor)
		VALUES ($1, $2, $3, 'active', $4, 'user:ada', 'search', 'supplier', now() + interval '30 days', now(), 'user:d')`, uuid.Must(uuid.NewV7()), lo, hi, buyer.ID); err != nil {
		t.Fatal(err)
	}
	if got := read(asApp(buyer.ID)); !left(got) {
		t.Fatalf("a linked buyer read %v, want %v", got, want)
	}
	if got := read(asApp(stranger.ID)); len(got) != 0 {
		t.Fatalf("a stranger read %v", got)
	}
	// A linked buyer reads nothing else of the shop's stock.
	if n := count(t, d, []uuid.UUID{buyer.ID}, `SELECT count(*) FROM stock_items WHERE tenant_id = $1`, shop.ID); n != 0 {
		t.Fatalf("a linked buyer read %d of the shop's stock items", n)
	}
}
