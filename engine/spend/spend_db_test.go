package spend_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/spend"
)

var ctx = context.Background()

type world struct {
	t      *testing.T
	d      *dbtest.DB
	tenant uuid.UUID
	log    *moneylog.Log
}

func newWorld(t *testing.T) *world {
	d := dbtest.New(t)
	return &world{t: t, d: d, tenant: d.SeedTenant(t, nil).ID, log: &moneylog.Log{Pool: d.App}}
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(ctx, w.d.App, []uuid.UUID{w.tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) label(name string) spend.Label {
	w.t.Helper()
	var l spend.Label
	w.in(func(tx pgx.Tx) error {
		var err error
		l, _, err = spend.FindOrMakeTx(ctx, tx, w.tenant, name, "user:ada")
		return err
	})
	return l
}

// supplier adds a supplier, a stock item and a confirmed order with no
// lines yet.
func (w *world) order(ref string) (order, supplier, item uuid.UUID) {
	w.t.Helper()
	order, supplier, item = uuid.New(), uuid.New(), uuid.New()
	w.exec(`INSERT INTO suppliers (id, tenant_id, name, phone_hash, phone, created_by) VALUES ($1, $2, $3, $4, '{}', 'user:ada')`,
		supplier, w.tenant, "Singer "+ref, bytes.Repeat([]byte(ref[2:3]), 32))
	w.exec(`INSERT INTO stock_items (id, tenant_id, name, created_by) VALUES ($1, $2, 'Ankara fabric', 'user:ada')`, item, w.tenant)
	w.exec(`INSERT INTO purchase_orders (id, tenant_id, reference, supplier_id, total_kobo, status, drafted_by, approved_by, sent_at, key)
		VALUES ($1, $2, $3, $4, 0, 'confirmed', 'user:ada', 'user:ada', now(), $3)`, order, w.tenant, ref, supplier)
	return order, supplier, item
}

func (w *world) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.d.Admin.Exec(ctx, q, args...); err != nil {
		w.t.Fatal(err)
	}
}

// line adds a purchase line; label and seller may be nil.
func (w *world) line(order uuid.UUID, pos int, name string, qty, cost int64, treatment string, stockItem, label *uuid.UUID, seller *string) {
	w.t.Helper()
	w.exec(`INSERT INTO purchase_lines (id, tenant_id, purchase_order_id, position, name, quantity, cost_kobo, treatment, stock_item_id, label_id, seller_item_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, uuid.New(), w.tenant, order, pos, name, qty, cost, treatment, stockItem, label, seller)
	w.exec(`UPDATE purchase_orders SET total_kobo = total_kobo + $2 WHERE id = $1`, order, qty*cost)
}

type entry struct {
	Amount   int64
	Category string
	Label    *uuid.UUID
	Vendor   string
	Ref      string
}

func (w *world) entries() []entry {
	w.t.Helper()
	rows, err := w.d.Admin.Query(ctx, `SELECT amount_kobo, COALESCE(category, ''), label_id, COALESCE(vendor, ''), reference FROM money_entries
		WHERE tenant_id = $1 ORDER BY recorded_at, reference`, w.tenant)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[entry])
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func ptr[T any](v T) *T { return &v }

// Labels are made by typing them, found without case; a merged label's
// name finds the label kept; a rename refuses another label's name; a
// merge moves the labels merged into the one merged first.
func TestLabels(t *testing.T) {
	w := newWorld(t)
	snacks := w.label("  Snacks ")
	if snacks.Name != "Snacks" {
		t.Fatalf("made: %+v", snacks)
	}
	var again spend.Label
	var made bool
	w.in(func(tx pgx.Tx) error {
		var err error
		again, made, err = spend.FindOrMakeTx(ctx, tx, w.tenant, "SNACKS", "user:bayo")
		return err
	})
	if made || again.ID != snacks.ID {
		t.Fatalf("found again: %+v %v", again, made)
	}
	welfare, treats, kept := w.label("Staff welfare"), w.label("Treats"), w.label("Team")
	w.in(func(tx pgx.Tx) error {
		if _, err := spend.RenameTx(ctx, tx, w.tenant, snacks.ID, "staff WELFARE"); !errors.Is(err, spend.ErrLabel) {
			t.Errorf("rename to another label's name: %v", err)
		}
		return nil
	})
	w.in(func(tx pgx.Tx) error {
		l, err := spend.RenameTx(ctx, tx, w.tenant, snacks.ID, "Shop snacks")
		if err != nil || l.Name != "Shop snacks" {
			t.Errorf("rename: %+v %v", l, err)
		}
		// Snacks and Treats into Staff welfare.
		if _, err := spend.MergeTx(ctx, tx, w.tenant, snacks.ID, welfare.ID); err != nil {
			return err
		}
		if _, err := spend.MergeTx(ctx, tx, w.tenant, treats.ID, welfare.ID); err != nil {
			return err
		}
		if _, err := spend.MergeTx(ctx, tx, w.tenant, welfare.ID, welfare.ID); !errors.Is(err, spend.ErrLabel) {
			t.Errorf("into itself: %v", err)
		}
		// Typing a merged label's name finds the label kept.
		if l, err := spend.FindTx(ctx, tx, w.tenant, "shop snacks"); err != nil || l.ID != welfare.ID {
			t.Errorf("a merged name: %+v %v", l, err)
		}
		if _, err := spend.RenameTx(ctx, tx, w.tenant, snacks.ID, "Biscuits"); !errors.Is(err, spend.ErrLabel) {
			t.Errorf("rename a merged label: %v", err)
		}
		// Staff welfare into Team: Snacks and Treats move to Team first.
		l, err = spend.MergeTx(ctx, tx, w.tenant, welfare.ID, kept.ID)
		if err != nil || l.MergedInto == nil || *l.MergedInto != kept.ID {
			t.Errorf("merge a label others are merged into: %+v %v", l, err)
		}
		// Into a merged label: into the one it is kept as.
		biscuits, _, err := spend.FindOrMakeTx(ctx, tx, w.tenant, "Biscuits", "user:ada")
		if err != nil {
			return err
		}
		if l, err = spend.MergeTx(ctx, tx, w.tenant, biscuits.ID, snacks.ID); err != nil || *l.MergedInto != kept.ID {
			t.Errorf("into a merged label: %+v %v", l, err)
		}
		return nil
	})
	var chained int
	if err := w.d.Admin.QueryRow(ctx, `SELECT count(*) FROM spend_labels l JOIN spend_labels k ON k.id = l.merged_into WHERE k.merged_into IS NOT NULL`).Scan(&chained); err != nil || chained != 0 {
		t.Fatalf("chains: %d %v", chained, err)
	}
	w.in(func(tx pgx.Tx) error {
		list, err := spend.ListTx(ctx, tx, w.tenant)
		if err != nil {
			return err
		}
		if len(list) != 1 || list[0].ID != kept.ID {
			t.Errorf("list: %+v", list)
		}
		return nil
	})
}

// The §12 order paid: in full, three entries adding up to it, the stock
// line labelled Stock bought; in part, shared by label with the kobo
// left over to the largest; the same key again writes nothing.
func TestPurchaseExpense(t *testing.T) {
	w := newWorld(t)
	order, _, fabric := w.order("P-AAA111")
	machines, packaging := w.label("Machines"), w.label("Packaging")
	w.line(order, 0, "Ankara fabric", 20, 200_000, spend.Stock, &fabric, nil, nil)
	w.line(order, 1, "Nylon bags", 100, 12_000, spend.Record, nil, &packaging.ID, nil)
	w.line(order, 2, "Singer sewing machine", 1, 18_000_000, spend.Track, nil, &machines.ID, nil)
	pay := func(key string, amount int64) ([]spend.Paid, bool) {
		var paid []spend.Paid
		var fresh bool
		w.in(func(tx pgx.Tx) error {
			var err error
			paid, fresh, err = spend.RecordPurchaseTx(ctx, tx, w.log, w.tenant, spend.PurchasePay{Order: order, Reference: "P-AAA111", Supplier: "Singer Ltd",
				AmountKobo: amount, How: "transfer", Channel: "bank", Key: key, Actor: "user:ada", When: time.Now()})
			return err
		})
		return paid, fresh
	}
	paid, fresh := pay("k1", 23_200_000)
	if !fresh || len(paid) != 3 {
		t.Fatalf("paid: %+v %v", paid, fresh)
	}
	got := w.entries()
	var stockBought uuid.UUID
	if err := w.d.Admin.QueryRow(ctx, `SELECT id FROM spend_labels WHERE name = 'Stock bought'`).Scan(&stockBought); err != nil {
		t.Fatal(err)
	}
	want := []entry{{4_000_000, "Stock bought", &stockBought, "Singer Ltd", "purchase:k1"}, {1_200_000, "Packaging", &packaging.ID, "Singer Ltd", "purchase:k1/2"},
		{18_000_000, "Machines", &machines.ID, "Singer Ltd", "purchase:k1/3"}}
	if len(got) != 3 {
		t.Fatalf("entries: %+v", got)
	}
	for i := range want {
		g := got[i]
		if g.Amount != want[i].Amount || g.Category != want[i].Category || *g.Label != *want[i].Label || g.Vendor != want[i].Vendor || g.Ref != want[i].Ref {
			t.Errorf("entry %d: %+v, want %+v", i, g, want[i])
		}
	}
	// The default is kept on the line.
	var labelled uuid.UUID
	if err := w.d.Admin.QueryRow(ctx, `SELECT label_id FROM purchase_lines WHERE purchase_order_id = $1 AND position = 0`, order).Scan(&labelled); err != nil || labelled != stockBought {
		t.Fatalf("the stock line's label: %v %v", labelled, err)
	}
	var rows, sum int64
	if err := w.d.Admin.QueryRow(ctx, `SELECT count(*), sum(amount_kobo) FROM purchase_payments WHERE purchase_id = $1`, order).Scan(&rows, &sum); err != nil || rows != 3 || sum != 23_200_000 {
		t.Fatalf("payments: %d %d %v", rows, sum, err)
	}
	// Again under the same key: nothing new.
	if _, fresh := pay("k1", 23_200_000); fresh || len(w.entries()) != 3 {
		t.Fatalf("repeated: %v %d", fresh, len(w.entries()))
	}

	// A part payment of ₦100,000 on another order the same.
	order2, _, fabric2 := w.order("P-BBB222")
	w.line(order2, 0, "Ankara fabric", 20, 200_000, spend.Stock, &fabric2, nil, nil)
	w.line(order2, 1, "Nylon bags", 100, 12_000, spend.Record, nil, &packaging.ID, nil)
	w.line(order2, 2, "Singer sewing machine", 1, 18_000_000, spend.Track, nil, &machines.ID, nil)
	order = order2
	paid, fresh = pay("k2", 10_000_000)
	if !fresh || len(paid) != 3 || paid[0].Kobo != 1_724_137 || paid[1].Kobo != 517_241 || paid[2].Kobo != 7_758_622 {
		t.Fatalf("a part: %+v", paid)
	}
	if err := w.d.Admin.QueryRow(ctx, `SELECT sum(amount_kobo) FROM money_entries WHERE reference LIKE 'purchase:k2%'`).Scan(&sum); err != nil || sum != 10_000_000 {
		t.Fatalf("a part adds up: %d %v", sum, err)
	}
	// What each entry bought, read back from the order.
	w.in(func(tx pgx.Tx) error {
		parts, ok, err := spend.EntryPartsTx(ctx, tx, w.tenant, paid[2].EntryID, paid[2].LabelID, paid[2].Kobo)
		if err != nil || !ok || len(parts) != 1 || parts[0].Treatment != spend.Track || parts[0].Kobo != 7_758_622 {
			t.Errorf("the machines' entry: %+v %v %v", parts, ok, err)
		}
		parts, ok, err = spend.EntryPartsTx(ctx, tx, w.tenant, paid[0].EntryID, paid[0].LabelID, paid[0].Kobo)
		if err != nil || !ok || len(parts) != 1 || parts[0].Treatment != spend.Stock {
			t.Errorf("the stock entry: %+v %v %v", parts, ok, err)
		}
		return nil
	})
	// An entry was never changed: the log refuses it.
	if _, err := w.d.App.Exec(ctx, `UPDATE money_entries SET label_id = NULL WHERE id = $1`, paid[0].EntryID); err == nil {
		t.Fatal("an entry changed")
	}
}

// A linked supplier's bill that does not match its order: lines matched
// by seller item take the order line's label and treatment at the bill's
// price; a delivery charge is Not labelled; the order line the bill does
// not have adds nothing. A bill with no order is split by item links.
func TestBillParts(t *testing.T) {
	w := newWorld(t)
	seller := w.d.SeedTenant(t, nil).ID
	a, b := w.tenant, seller
	if b.String() < a.String() {
		a, b = b, a
	}
	link := uuid.New()
	w.exec(`INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at)
		VALUES ($1, $2, $3, 'active', $2, 'user:ada', 'code', 'both', now() + interval '7 days')`, link, a, b)
	order, _, fabric := w.order("P-CCC333")
	machines, packaging, fabricL, fuel := w.label("Machines"), w.label("Packaging"), w.label("Fabric"), w.label("Fuel")
	w.line(order, 0, "Ankara fabric", 20, 200_000, spend.Stock, &fabric, &fabricL.ID, ptr("it-fabric"))
	w.line(order, 1, "Singer sewing machine", 1, 18_000_000, spend.Track, nil, &machines.ID, ptr("it-machine"))
	w.line(order, 2, "Nylon bags", 100, 12_000, spend.Record, nil, &packaging.ID, ptr("it-bags"))
	bill := func(lines []map[string]any) uuid.UUID {
		raw, _ := json.Marshal(lines)
		id := uuid.New()
		var total int64
		for _, l := range lines {
			total += l["quantity"].(int64) * l["unit_kobo"].(int64)
		}
		w.exec(`INSERT INTO bills (id, tenant_id, link_id, supplier_tenant, supplier_name, source_id, reference, lines, amount_kobo)
			VALUES ($1, $2, $3, $4, 'Singer Ltd', $5, $6, $7, $8)`, id, w.tenant, link, seller, uuid.New(), "INV-"+id.String()[:4], raw, total)
		return id
	}
	// The bill: fabric at ₦2,100 (not the order's ₦2,000), the machine,
	// a delivery charge; no bags.
	matched := bill([]map[string]any{
		{"description": "Ankara fabric", "quantity": int64(20), "unit_kobo": int64(210_000), "seller_item_id": "it-fabric", "purchase_id": order},
		{"description": "Singer sewing machine", "quantity": int64(1), "unit_kobo": int64(18_000_000), "seller_item_id": "it-machine", "purchase_id": order},
		{"description": "Delivery", "quantity": int64(1), "unit_kobo": int64(500_000)},
	})
	w.in(func(tx pgx.Tx) error {
		parts, err := spend.BillPartsTx(ctx, tx, w.tenant, matched)
		if err != nil {
			return err
		}
		if len(parts) != 3 || *parts[0].LabelID != fabricL.ID || parts[0].Treatment != spend.Stock || parts[0].Kobo != 4_200_000 ||
			*parts[1].LabelID != machines.ID || parts[1].Treatment != spend.Track || parts[1].Kobo != 18_000_000 ||
			parts[2].LabelID != nil || parts[2].Treatment != spend.Record || parts[2].Kobo != 500_000 {
			t.Errorf("matched: %+v", parts)
		}
		return nil
	})
	// No order behind it: the item link of a line, else Not labelled.
	w.exec(`INSERT INTO item_links (tenant_id, link_id, seller_item_id, seller_item, treatment, label_id, updated_by) VALUES ($1, $2, 'it-diesel', 'Diesel', 'record', $3, 'user:ada')`,
		w.tenant, link, fuel.ID)
	loose := bill([]map[string]any{
		{"description": "Diesel", "quantity": int64(2), "unit_kobo": int64(1_000_000), "seller_item_id": "it-diesel"},
		{"description": "Jerrycan", "quantity": int64(1), "unit_kobo": int64(300_000), "seller_item_id": "it-can"},
	})
	w.in(func(tx pgx.Tx) error {
		parts, err := spend.BillPartsTx(ctx, tx, w.tenant, loose)
		if err != nil {
			return err
		}
		if len(parts) != 2 || *parts[0].LabelID != fuel.ID || parts[0].Kobo != 2_000_000 || parts[1].LabelID != nil || parts[1].Kobo != 300_000 {
			t.Errorf("no order: %+v", parts)
		}
		return nil
	})
}
