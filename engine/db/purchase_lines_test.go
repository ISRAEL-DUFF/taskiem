package db_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
)

// Purchases on the server (00503, what a business buys §5, §6, §8): what
// the schema itself holds, whatever the engine does.
func TestPurchaseLinesSchema(t *testing.T) {
	d := dbtest.New(t)
	shop := d.SeedTenant(t, nil)
	other := d.SeedTenant(t, nil)
	supplier, order, item, otherItem := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	machines, kit, tools := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO suppliers (id, tenant_id, name, phone_hash, phone, created_by) VALUES ($1, $2, 'Singer Ltd', sha256('p'), '{}', 'user:ada')`, []any{supplier, shop.ID}},
		{`INSERT INTO purchase_orders (id, tenant_id, reference, supplier_id, total_kobo, drafted_by, key) VALUES ($1, $2, 'P-AAAAAA', $3, 0, 'user:ada', 'k')`, []any{order, shop.ID, supplier}},
		{`INSERT INTO stock_items (id, tenant_id, name, created_by) VALUES ($1, $2, 'Thread', 'user:ada')`, []any{item, shop.ID}},
		{`INSERT INTO stock_items (id, tenant_id, name, created_by) VALUES ($1, $2, 'Theirs', 'user:ada')`, []any{otherItem, other.ID}},
		{`INSERT INTO spend_labels (id, tenant_id, name, created_by) VALUES ($1, $4, 'Machines', 'user:ada'), ($2, $4, 'Kit', 'user:ada'), ($3, $4, 'Tools', 'user:ada')`, []any{machines, kit, tools, shop.ID}},
	} {
		if _, err := d.Admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	app := d.AppPool(t, 2)
	as := func(tenant uuid.UUID, sql string, args ...any) error {
		return db.InTenantTx(ctx, app, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, sql, args...)
			return err
		})
	}
	refused := func(what, want string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want an error with %q, got %v", what, want, err)
		}
	}
	line := `INSERT INTO purchase_lines (id, tenant_id, purchase_order_id, position, name, quantity, cost_kobo, treatment, stock_item_id, label_id)
		VALUES ($1, $2, $3, $4, $5, $6, 1000, $7, $8, $9)`

	// A stock line names its stock item, and only a stock line does; a
	// line over 200 is not tracked; a stock item is the business's own.
	refused("stock with no stock item", "check", as(shop.ID, line, uuid.New(), shop.ID, order, 0, "Thread", 5, "stock", nil, nil))
	refused("record with a stock item", "check", as(shop.ID, line, uuid.New(), shop.ID, order, 0, "Thread", 5, "record", item, nil))
	refused("track over 200", "check", as(shop.ID, line, uuid.New(), shop.ID, order, 0, "Bags", 201, "track", nil, nil))
	refused("another business's stock item", "foreign key", as(shop.ID, line, uuid.New(), shop.ID, order, 0, "Theirs", 5, "stock", otherItem, nil))
	stockLine, trackLine := uuid.New(), uuid.New()
	if err := as(shop.ID, line, stockLine, shop.ID, order, 0, "Thread", 5, "stock", item, nil); err != nil {
		t.Fatal(err)
	}
	if err := as(shop.ID, line, trackLine, shop.ID, order, 1, "Sewing machine", 2, "track", nil, machines); err != nil {
		t.Fatal(err)
	}
	refused("two lines in one place", "duplicate", as(shop.ID, line, uuid.New(), shop.ID, order, 1, "Oil", 1, "record", nil, nil))
	// Another business sees none of it.
	var n int
	if err := db.InTenantTx(ctx, app, []uuid.UUID{other.ID}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM purchase_lines`).Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("another business reads %d lines (%v)", n, err)
	}

	// Until something is received the treatment changes; after, it is
	// locked and the line stays, though its label may still change.
	if err := as(shop.ID, `UPDATE purchase_lines SET treatment = 'record', stock_item_id = NULL WHERE id = $1`, stockLine); err != nil {
		t.Fatal(err)
	}
	if err := as(shop.ID, `UPDATE purchase_lines SET treatment = 'stock', stock_item_id = $2, received = 2 WHERE id = $1`, stockLine, item); err != nil {
		t.Fatal(err)
	}
	refused("treatment after receiving", "locked", as(shop.ID, `UPDATE purchase_lines SET treatment = 'record', stock_item_id = NULL WHERE id = $1`, stockLine))
	refused("uses after receiving", "locked", as(shop.ID, `UPDATE purchase_lines SET uses = 6 WHERE id = $1`, stockLine))
	refused("removed after receiving", "not removed", as(shop.ID, `DELETE FROM purchase_lines WHERE id = $1`, stockLine))
	if err := as(shop.ID, `UPDATE purchase_lines SET label_id = $2, received = 5 WHERE id = $1`, stockLine, kit); err != nil {
		t.Fatal(err)
	}

	// Things we have: each unit of a tracked line once.
	thing := `INSERT INTO kept_things (id, tenant_id, name, label_id, supplier_id, purchase_order_id, purchase_line_id, unit_no, cost_kobo, received_at, created_by)
		VALUES (gen_random_uuid(), $1, 'Sewing machine', $2, $3, $4, $5, $6, 1000, now(), 'user:ada') ON CONFLICT (tenant_id, purchase_line_id, unit_no) DO NOTHING`
	for range 2 {
		for u := 1; u <= 2; u++ {
			if err := as(shop.ID, thing, shop.ID, machines, supplier, order, trackLine, u); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := as(shop.ID, `INSERT INTO kept_things (id, tenant_id, name, created_by) VALUES (gen_random_uuid(), $1, 'Generator', 'user:ada')`, shop.ID); err != nil {
		t.Fatal(err)
	}
	var things, moved int
	if err := as(shop.ID, `UPDATE kept_things SET status = 'broken' WHERE unit_no = 1`); err != nil {
		t.Fatal(err)
	}
	if err := d.Admin.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status = 'broken' AND status_at > created_at) FROM kept_things WHERE tenant_id = $1`, shop.ID).Scan(&things, &moved); err != nil || things != 3 || moved != 1 {
		t.Fatalf("things %d, status moved %d (%v)", things, moved, err)
	}
	refused("a unit with no line", "check", as(shop.ID, `INSERT INTO kept_things (id, tenant_id, name, unit_no, created_by) VALUES (gen_random_uuid(), $1, 'X', 1, 'user:ada')`, shop.ID))

	// Labels: one name per business without case; a merge is one step.
	refused("a label's name twice", "duplicate", as(shop.ID, `INSERT INTO spend_labels (id, tenant_id, name, created_by) VALUES (gen_random_uuid(), $1, 'machines', 'user:ada')`, shop.ID))
	if err := as(other.ID, `INSERT INTO spend_labels (id, tenant_id, name, created_by) VALUES (gen_random_uuid(), $1, 'Machines', 'user:ada')`, other.ID); err != nil {
		t.Fatalf("another business's own Machines: %v", err)
	}
	if err := as(shop.ID, `UPDATE spend_labels SET merged_into = $2 WHERE id = $1`, kit, machines); err != nil {
		t.Fatal(err)
	}
	refused("into a merged label", "not itself merged", as(shop.ID, `UPDATE spend_labels SET merged_into = $2 WHERE id = $1`, tools, kit))
	refused("a label others are merged into", "move them first", as(shop.ID, `UPDATE spend_labels SET merged_into = $2 WHERE id = $1`, machines, tools))

	// The money log: an entry is made with its label and never changed.
	entry := uuid.New()
	if err := as(shop.ID, `INSERT INTO money_entries (id, tenant_id, entry_key, direction, kind, amount_kobo, status, channel, reference, source, category, label_id)
		VALUES ($1, $2, 'e1', 'out', 'expense', 1000, 'settled', 'cash', 'P-AAAAAA', 'entered', 'Machines', $3)`, entry, shop.ID, machines); err != nil {
		t.Fatal(err)
	}
	refused("relabelled by the app", "permission denied", as(shop.ID, `UPDATE money_entries SET label_id = $2 WHERE id = $1`, entry, tools))
	refused("relabelled while copied", "never changed", func() error {
		_, err := d.Admin.Exec(ctx, `UPDATE money_entries SET label_id = $2, mirrored_at = now() WHERE id = $1`, entry, tools)
		return err
	}())
	if err := as(shop.ID, `UPDATE money_entries SET mirrored_at = now() WHERE id = $1`, entry); err != nil {
		t.Fatalf("marked copied: %v", err)
	}

	// The memories of a supplier's item hold the stock item with the
	// treatment; supplier_items holds only keys made as stock.NameKey does.
	refused("item link: stock with none", "check", as(shop.ID, `INSERT INTO item_links (tenant_id, link_id, seller_item_id, seller_item, treatment, updated_by)
		VALUES ($1, gen_random_uuid(), 's1', 'Thread', 'stock', 'user:ada')`, shop.ID))
	refused("a name key not made so", "check", as(shop.ID, `INSERT INTO supplier_items (tenant_id, supplier_id, name_key, name, treatment, updated_by)
		VALUES ($1, $2, 'Diesel ', 'Diesel ', 'record', 'user:ada')`, shop.ID, supplier))
	if err := as(shop.ID, `INSERT INTO supplier_items (tenant_id, supplier_id, name_key, name, treatment, label_id, updated_by)
		VALUES ($1, $2, 'diesel', 'Diesel ', 'record', $3, 'user:ada')`, shop.ID, supplier, tools); err != nil {
		t.Fatal(err)
	}
	refused("another business's supplier", "foreign key", as(other.ID, `INSERT INTO supplier_items (tenant_id, supplier_id, name_key, name, treatment, updated_by)
		VALUES ($1, $2, 'diesel', 'Diesel', 'record', 'user:ada')`, other.ID, supplier))
}
