package stock_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/leaks"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// catalogue is the fake table read as rows, for the watch: each item's
// count, with the reorder levels given.
type catalogue struct {
	*table
	reorder map[string]int64
}

func (c *catalogue) Rows(context.Context, uuid.UUID) ([]map[string]any, storefront.Columns, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var rows []map[string]any
	for id, n := range c.n {
		r := map[string]any{"id": id, "name": strings.ToUpper(id[:1]) + id[1:], "stock": n}
		if lvl, ok := c.reorder[id]; ok {
			r["reorder_level"] = lvl
		}
		rows = append(rows, r)
	}
	return rows, storefront.Columns{Name: "name", Stock: "stock", ReorderLevel: "reorder_level"}, nil
}

// A count posts its differences as count movements naming it, valued at
// cost; the same key is the same count; shrinkage adds up a period; the
// three stock leaks open, valued at cost, and stay until closed.
func TestCountsShrinkageAndLeaks(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0, "indomie": 0, "garri": 0})
	cost := int64(4000000)
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(20), CostKobo: &cost}).ID)
	w.apply(w.record(stock.Move{ItemID: "indomie", Item: "Indomie", Reason: stock.Received, Delta: n(10)}).ID)
	w.apply(w.record(stock.Move{ItemID: "garri", Item: "Garri", Reason: stock.Received, Delta: n(5)}).ID)
	// A sale of more Indomie than there is.
	x := w.sale(stock.SaleIn{PaidHow: stock.Cash, Key: "s-1", Lines: []stock.SaleLine{
		{ItemID: "indomie", Item: "Indomie", Quantity: 12, PriceKobo: 100000, CostKobo: n(85000)},
		{ItemID: "rice", Item: "Rice", Quantity: 2, PriceKobo: 4500000, CostKobo: &cost},
	}})
	if len(x.Short) != 1 {
		t.Fatalf("sale: %+v", x)
	}

	in := stock.CountIn{Full: true, Reason: "rats", Actor: "user:owner", Key: "c-1", Lines: []stock.CountLine{
		{ItemID: "rice", Item: "Rice", Unit: "bag", Expected: n(18), Counted: 15, CostKobo: &cost},
		{ItemID: "indomie", Item: "Indomie", Expected: n(0), Counted: 0},
		{ItemID: "garri", Item: "Garri", Expected: n(5), Counted: 4},
	}}
	c, err := w.s.RecordCount(ctx, w.env.Tenant, in)
	if err != nil || c.Changed != 2 || c.LostKobo != 3*cost || !c.Full || c.Reference[:2] != "C-" {
		t.Fatalf("count: %+v %v", c, err)
	}
	if w.tb.count("rice") != 15 || w.tb.count("garri") != 4 || w.ledger("rice") != 15 {
		t.Fatalf("after the count: rice %d, garri %d", w.tb.count("rice"), w.tb.count("garri"))
	}
	if m := w.movements("rice")[0]; m.Reason != stock.Count || m.Reference != c.Reference || m.Note != "rats" || *m.Change != -3 {
		t.Fatalf("movement: %+v", m)
	}
	if again, err := w.s.RecordCount(ctx, w.env.Tenant, in); err != nil || again.ID != c.ID || w.tb.count("rice") != 15 {
		t.Fatalf("again: %+v %v", again, err)
	}
	w.in(func(tx pgx.Tx) error {
		for _, bad := range []stock.CountIn{{Key: "c-2"}, {Key: "c-3", Lines: []stock.CountLine{{ItemID: "rice", Item: "Rice", Counted: -1}}},
			{Key: "c-4", Lines: []stock.CountLine{{ItemID: "rice", Item: "Rice"}, {ItemID: "rice", Item: "Rice"}}}} {
			if _, _, err := stock.CountTx(ctx, tx, w.env.Tenant, bad); !errors.Is(err, stock.ErrCount) {
				t.Errorf("%+v: %v", bad, err)
			}
		}
		list, err := stock.CountsTx(ctx, tx, w.env.Tenant, 10)
		if err != nil || len(list) != 1 || list[0].Reference != c.Reference {
			t.Errorf("counts: %+v %v", list, err)
		}
		return nil
	})

	// The table goes below zero by hand: seen on the next refresh.
	mark, _ := w.s.ReadMark(ctx)
	w.tb.n["garri"] = -2
	if _, err := w.s.Outside(ctx, w.env.Tenant, []stock.Seen{{ItemID: "garri", Item: "Garri", Count: -2}}, mark); err != nil {
		t.Fatal(err)
	}

	// Shrinkage: what came in, was sold, and was lost, at cost.
	w.in(func(tx pgx.Tx) error {
		sh, err := stock.ShrinkTx(ctx, tx, w.env.Tenant, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
		if err != nil {
			return err
		}
		if sh.Received != 35 || sh.Sold != 12 || sh.Lost != 4 || sh.LostKobo != 3*cost || sh.Worst().Item != "Rice" {
			t.Errorf("shrinkage: %+v", sh)
		}
		return nil
	})

	// The three leaks: the sale beyond the stock (2 Indomie at ₦850), the
	// count below zero, the count's loss above ₦5,000 (Rice; Garri has no
	// cost, so it is a leak in units).
	d := &leaks.Detector{Pool: w.env.DB.App}
	if err := d.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	type leak struct {
		kind, detail string
		amount       int64
	}
	var got []leak
	rows, err := w.env.DB.Admin.Query(ctx, `SELECT kind, detail, amount_kobo FROM money_leaks WHERE tenant_id = $1 AND kind LIKE 'stock_%' ORDER BY kind, detail`, w.env.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	got, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (leak, error) {
		var l leak
		return l, r.Scan(&l.kind, &l.detail, &l.amount)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].kind != "stock_count" || got[0].amount != 0 || !strings.Contains(got[0].detail, "a count found 1 Garri fewer") ||
		got[1].kind != "stock_count" || got[1].amount != 3*cost || !strings.Contains(got[1].detail, "3 Rice fewer than recorded ("+c.Reference+"), worth ₦120,000 at cost") ||
		got[2].kind != "stock_negative" || !strings.Contains(got[2].detail, "Garri went below zero (-2)") ||
		got[3].kind != "stock_short" || got[3].amount != 2*85000 || !strings.Contains(got[3].detail, "sold 2 more Indomie than the stock held ("+x.Reference+"), worth ₦1,700 at cost") {
		t.Fatalf("leaks: %+v", got)
	}
	// They stay until closed.
	if err := d.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var open int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM money_leaks WHERE tenant_id = $1 AND kind LIKE 'stock_%' AND status = 'open'`, w.env.Tenant).Scan(&open); err != nil || open != 4 {
		t.Fatalf("open: %d %v", open, err)
	}
}

// The watch: an item low a week with nothing ordered, and an order
// confirmed and not received after its days, are told to the owners once.
func TestStockWatch(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 2, "indomie": 40})
	cat := &catalogue{table: w.tb, reorder: map[string]int64{"rice": 5, "indomie": 10}}
	w.s.Table = cat
	now := time.Now()
	watch := &stock.Watch{Stock: w.s, Pool: w.env.DB.App, Now: func() time.Time { return now }}
	if n, err := watch.Tenant(ctx, w.env.Tenant); err != nil || n != 0 {
		t.Fatalf("first look: %d %v", n, err)
	}
	now = now.Add(8 * 24 * time.Hour)
	if n, err := watch.Tenant(ctx, w.env.Tenant); err != nil || n != 1 {
		t.Fatalf("a week low: %d %v", n, err)
	}
	if n, err := watch.Tenant(ctx, w.env.Tenant); err != nil || n != 0 {
		t.Fatalf("told once: %d %v", n, err)
	}
	// Back above, then low again: a new week.
	w.tb.n["rice"] = 20
	if _, err := watch.Tenant(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	var low int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM stock_low WHERE tenant_id = $1`, w.env.Tenant).Scan(&low); err != nil || low != 0 {
		t.Fatalf("low kept: %d %v", low, err)
	}

	// An order confirmed four days ago, expected in three.
	p := w.purchases()
	var po stock.Purchase
	w.in(func(tx pgx.Tx) error {
		s, err := p.AddSupplierTx(ctx, tx, w.env.Tenant, "Dangote Depot", "08031234567", "user:owner")
		if err != nil {
			return err
		}
		if po, err = stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: s.ID, Lines: []stock.PurchaseLine{{ItemID: "rice", Item: "Rice", Quantity: 10}},
			Actor: "user:owner", Key: "d"}); err != nil {
			return err
		}
		if po, err = p.SendTx(ctx, tx, w.env.Tenant, po.ID, "user:owner"); err != nil {
			return err
		}
		_, err = stock.ConfirmTx(ctx, tx, w.env.Tenant, po.ID, "owner", "user:owner")
		return err
	})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE purchase_orders SET confirmed_at = now() - interval '4 days' WHERE id = $1`, po.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := watch.Tenant(ctx, w.env.Tenant); err != nil || n != 1 {
		t.Fatalf("late order: %d %v", n, err)
	}
	var kinds []string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT array_agg(kind ORDER BY created_at) FROM alerts WHERE tenant_id = $1 AND kind LIKE 'stock.%'`, w.env.Tenant).Scan(&kinds); err != nil ||
		len(kinds) != 2 || kinds[0] != stock.AlertLowWeek || kinds[1] != stock.AlertLateBuy {
		t.Fatalf("alerts: %v %v", kinds, err)
	}
}
