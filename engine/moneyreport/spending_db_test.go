package moneyreport_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	"github.com/israel-duff/taskiem/engine/spend"
)

// Spending by label (what a business buys §5.4): the §12 order paid, two
// expenses entered by hand and a linked supplier's bill paid by payout,
// grouped by label; what was bought to keep left out of profit; a rename
// and a merge regroup the report at once, and no entry changes.
func TestSpendingByLabel(t *testing.T) {
	d := dbtest.New(t)
	tenant := d.SeedTenant(t, nil).ID
	log := &moneylog.Log{Pool: d.App}
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, d.App, []uuid.UUID{tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Admin.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	label := func(name string) uuid.UUID {
		t.Helper()
		var l spend.Label
		in(func(tx pgx.Tx) error {
			var err error
			l, _, err = spend.FindOrMakeTx(ctx, tx, tenant, name, "user:ada")
			return err
		})
		return l.ID
	}
	// The order: fabric to stock (no label: Stock bought when paid), nylon
	// bags recorded under Packaging, a machine tracked under Machines.
	order, supplier, fabric := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO suppliers (id, tenant_id, name, phone_hash, phone, created_by) VALUES ($1, $2, 'Singer Ltd', $3, '{}', 'user:ada')`, supplier, tenant, make([]byte, 32))
	exec(`INSERT INTO stock_items (id, tenant_id, name, created_by) VALUES ($1, $2, 'Ankara fabric', 'user:ada')`, fabric, tenant)
	exec(`INSERT INTO purchase_orders (id, tenant_id, reference, supplier_id, total_kobo, status, drafted_by, approved_by, sent_at, key)
		VALUES ($1, $2, 'P-AAA111', $3, 23200000, 'confirmed', 'user:ada', 'user:ada', now(), 'k')`, order, tenant, supplier)
	machines, packaging := label("Machines"), label("Packaging")
	for i, l := range []struct {
		name            string
		qty, cost       int64
		treatment       string
		stockItem, labl *uuid.UUID
	}{{"Ankara fabric", 20, 200_000, spend.Stock, &fabric, nil}, {"Nylon bags", 100, 12_000, spend.Record, nil, &packaging},
		{"Singer sewing machine", 1, 18_000_000, spend.Track, nil, &machines}} {
		exec(`INSERT INTO purchase_lines (id, tenant_id, purchase_order_id, position, name, quantity, cost_kobo, treatment, stock_item_id, label_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`, uuid.New(), tenant, order, i, l.name, l.qty, l.cost, l.treatment, l.stockItem, l.labl)
	}
	in(func(tx pgx.Tx) error {
		_, _, err := spend.RecordPurchaseTx(ctx, tx, log, tenant, spend.PurchasePay{Order: order, Reference: "P-AAA111", Supplier: "Singer Ltd",
			AmountKobo: 23_200_000, How: "cash", Channel: "cash", Key: "k1", Actor: "user:ada", When: time.Now()})
		return err
	})
	// Snacks and Staff welfare, entered by hand; ₦500,000 cash in.
	snacks, welfare := label("Snacks"), label("Staff welfare")
	for i, x := range []struct {
		kobo  int64
		label uuid.UUID
		name  string
	}{{300_000, snacks, "Snacks"}, {200_000, welfare, "Staff welfare"}} {
		in(func(tx pgx.Tx) error {
			_, _, err := log.EnterTx(ctx, tx, tenant, "user:ada", moneylog.Entered{Kind: "expense", AmountKobo: x.kobo, Description: x.name, LabelID: &x.label,
				Category: x.name, Key: "hand" + string(rune('0'+i))})
			return err
		})
	}
	if _, _, err := log.Enter(ctx, tenant, "user:ada", moneylog.Entered{Kind: "cash", Direction: "in", AmountKobo: 50_000_000, Description: "Sales", Key: "till"}); err != nil {
		t.Fatal(err)
	}
	// A linked supplier's bill with no order: diesel under Fuel by its item
	// link, a delivery charge Not labelled; paid ₦25,000 by payout.
	seller := d.SeedTenant(t, nil).ID
	a, b := tenant, seller
	if b.String() < a.String() {
		a, b = b, a
	}
	link, bill, fuel := uuid.New(), uuid.New(), label("Fuel")
	exec(`INSERT INTO business_links (id, a_tenant, b_tenant, status, requested_by, requester_actor, how, role, expires_at)
		VALUES ($1, $2, $3, 'active', $2, 'user:ada', 'code', 'both', now() + interval '7 days')`, link, a, b)
	exec(`INSERT INTO item_links (tenant_id, link_id, seller_item_id, seller_item, treatment, label_id, updated_by) VALUES ($1, $2, 'it-diesel', 'Diesel', 'record', $3, 'user:ada')`,
		tenant, link, fuel)
	lines, _ := json.Marshal([]map[string]any{{"description": "Diesel", "quantity": 2, "unit_kobo": 1_000_000, "seller_item_id": "it-diesel"},
		{"description": "Delivery", "quantity": 1, "unit_kobo": 500_000}})
	exec(`INSERT INTO bills (id, tenant_id, link_id, supplier_tenant, supplier_name, source_id, reference, lines, amount_kobo)
		VALUES ($1, $2, $3, $4, 'Total Depot', $5, 'INV-0001', $6, 2500000)`, bill, tenant, link, seller, uuid.New(), lines)
	exec(`INSERT INTO bill_payments (id, tenant_id, bill_id, amount_kobo, token, connection, bank_code, account_number, status, created_by, payout_ref)
		VALUES ($1, $2, $3, 2500000, 'INV-0001/ABCD', 'main', '058', '0123456789', 'paid', 'user:ada', 'TR-1')`, uuid.New(), tenant, bill)
	payout := int64(2_500_000)
	if _, _, err := log.Record(ctx, tenant, moneylog.Entry{Direction: "out", Kind: "payout", Status: "settled", Channel: "paystack/main", Reference: "TR-1",
		Event: "payout.settled", AmountKobo: &payout, Source: "event", Description: "INV-0001/ABCD"}); err != nil {
		t.Fatal(err)
	}

	from, to := moneyreport.Day(time.Now())
	report := func() moneyreport.Spending {
		t.Helper()
		var s moneyreport.Spending
		in(func(tx pgx.Tx) error {
			var err error
			s, err = moneyreport.SpendingOf(ctx, tx, tenant, from, to)
			return err
		})
		return s
	}
	byName := func(s moneyreport.Spending) map[string]moneyreport.LabelSpend {
		m := map[string]moneyreport.LabelSpend{}
		for _, l := range s.Labels {
			m[l.Name] = l
		}
		return m
	}
	s := report()
	g := byName(s)
	if len(s.Labels) != 7 || g["Machines"].ToKeepKobo != 18_000_000 || g["Stock bought"].ToSellKobo != 4_000_000 || g["Packaging"].RunningKobo != 1_200_000 ||
		g["Snacks"].Kobo != 300_000 || g["Staff welfare"].Kobo != 200_000 || g["Fuel"].RunningKobo != 2_000_000 || g[spend.NotLabelled].Kobo != 500_000 ||
		g[spend.NotLabelled].LabelID != nil || s.Labels[0].Name != "Machines" {
		t.Fatalf("by label: %+v", s.Labels)
	}
	total := int64(23_200_000 + 500_000 + 2_500_000)
	if s.TotalKobo != total || s.ToKeepKobo != 18_000_000 || s.ToSellKobo != 4_000_000 || s.RunningKobo != total-22_000_000 {
		t.Fatalf("totals: %+v", s)
	}
	// Profit leaves out the machine (Bought to keep), and counts the
	// fabric (Bought to sell or make with) as stock bought.
	if s.Profit.InKobo != 50_000_000 || s.Profit.CostsKobo != total-18_000_000 || s.Profit.ProfitKobo != 50_000_000-(total-18_000_000) {
		t.Fatalf("profit: %+v", s.Profit)
	}

	// Packaging renamed Bags; Snacks merged into Staff welfare.
	in(func(tx pgx.Tx) error {
		if _, err := spend.RenameTx(ctx, tx, tenant, packaging, "Bags"); err != nil {
			return err
		}
		_, err := spend.MergeTx(ctx, tx, tenant, snacks, welfare)
		return err
	})
	g = byName(report())
	if _, ok := g["Packaging"]; ok || g["Bags"].Kobo != 1_200_000 || *g["Bags"].LabelID != packaging {
		t.Fatalf("renamed: %+v", g)
	}
	if _, ok := g["Snacks"]; ok || g["Staff welfare"].Kobo != 500_000 || g["Staff welfare"].Entries != 2 {
		t.Fatalf("merged: %+v", g)
	}
	// The log is as it was: each entry its label's id and name then.
	var cats []string
	if err := d.Admin.QueryRow(ctx, `SELECT array_agg(category ORDER BY category) FROM money_entries WHERE tenant_id = $1 AND label_id IN ($2, $3)`,
		tenant, packaging, snacks).Scan(&cats); err != nil || len(cats) != 2 || cats[0] != "Packaging" || cats[1] != "Snacks" {
		t.Fatalf("entries: %v %v", cats, err)
	}
}
