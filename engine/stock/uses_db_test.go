package stock_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/productstock"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// stockItem makes a stock item of the business's, unit unit, received at
// count, and counts each product of uses by it.
func (w *world) stockItem(name, unit string, count int64, uses map[string]int64) uuid.UUID {
	w.t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	w.in(func(tx pgx.Tx) error {
		x, err := stock.CreateItemTx(ctx, tx, w.env.Tenant, stock.NewItem{Name: name, Unit: unit, Actor: "user:owner"})
		if err != nil {
			return err
		}
		id = x.ID
		for product, u := range uses {
			if err := productstock.LinkTx(ctx, tx, w.env.Tenant, product, x.ID, u); err != nil {
				return err
			}
		}
		return nil
	})
	if count > 0 {
		w.record(stock.Move{ItemID: id.String(), Item: name, Reason: stock.Received, Delta: &count})
	}
	return id
}

func (w *world) countOf(id uuid.UUID) (n int64, cost *int64) {
	w.t.Helper()
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count, cost_kobo FROM stock_items WHERE id = $1`, id).Scan(&n, &cost)
	})
	return n, cost
}

// Rice counted in grams (what a business buys §12): a "1 kg" product uses
// 1000 and a "500 g" one 500, and a sale of one of each lowers the stock
// item by 1500, in the sale's transaction, once however often it is
// recorded. A sale beyond the count is recorded, short, never refused; the
// check before selling compares the quantity times the uses.
func TestRiceCountedInGrams(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	rice := w.stockItem("Rice", "g", 10000, map[string]int64{"rice-1kg": 1000, "rice-500g": 500})

	in := stock.SaleIn{PaidHow: stock.Cash, Key: "rice-sale", Lines: []stock.SaleLine{
		{ItemID: "rice-1kg", Item: "Rice 1 kg", Quantity: 1, PriceKobo: 150000},
		{ItemID: "rice-500g", Item: "Rice 500 g", Quantity: 1, PriceKobo: 80000},
	}}
	x := w.sale(in)
	if got, _ := w.countOf(rice); got != 8500 || len(x.Short) != 0 {
		t.Fatalf("after one of each: %d, short %v; want 8500", got, x.Short)
	}
	var moves []stock.Movement
	w.in(func(tx pgx.Tx) error {
		var err error
		moves, err = stock.ListTx(ctx, tx, w.env.Tenant, stock.Filter{ItemID: rice.String(), Reason: stock.Sale})
		return err
	})
	if len(moves) != 2 || *moves[0].Change+*moves[1].Change != -1500 || moves[0].Reference != x.Reference {
		t.Fatalf("the sale's movements: %+v", moves)
	}
	// The same key again: the same sale, nothing moves twice.
	if again := w.sale(in); again.ID != x.ID {
		t.Fatalf("replayed: %+v", again)
	}
	if got, _ := w.countOf(rice); got != 8500 {
		t.Fatalf("replayed sale moved stock: %d", got)
	}

	// The check before selling: 9 kg is more than the 8500 g there are.
	var items []stock.Item
	w.in(func(tx pgx.Tx) error {
		var err error
		items, err = stock.ItemsTx(ctx, tx, w.env.Tenant, []map[string]any{{"id": "rice-1kg", "name": "Rice 1 kg"}, {"id": "rice-500g", "name": "Rice 500 g"}},
			storefront.Columns{Name: "name"})
		return err
	})
	var kg stock.Item
	for _, it := range items {
		if it.ID == "rice-1kg" {
			kg = it
		}
	}
	if kg.Uses != 1000 || kg.StockItemID != rice.String() || !kg.Beyond(9) || kg.Beyond(8) || kg.SaleUnit() != "" {
		t.Fatalf("the 1 kg product: %+v", kg)
	}
	// Sold all the same: down to 0, 500 g short.
	x = w.sale(stock.SaleIn{PaidHow: stock.Cash, Lines: []stock.SaleLine{{ItemID: "rice-1kg", Item: "Rice 1 kg", Quantity: 9, PriceKobo: 150000}}})
	if got, _ := w.countOf(rice); got != 0 || len(x.Short) != 1 {
		t.Fatalf("beyond the count: %d, short %v", got, x.Short)
	}
}

// An accepted order lowers each product's stock item by its quantity
// times its uses, once; put back, the same comes back.
func TestOrderLinesByUses(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	malt := w.stockItem("Malt", "bottle", 20, map[string]int64{"pack": 6, "single": 1})
	order := uuid.New()
	lines := []storefront.Line{{ID: "pack", Name: "Malt, pack of 6", Quantity: 2}, {ID: "single", Name: "Malt", Quantity: 3}}
	for range 2 {
		short, err := w.s.OrderLines(ctx, w.env.Tenant, order, "AD-1", lines)
		if err != nil || len(short) != 0 {
			t.Fatalf("order: %v %v", short, err)
		}
		if got, _ := w.countOf(malt); got != 5 {
			t.Fatalf("after 2 packs and 3 singles: %d, want 5", got)
		}
	}
	// Another pack with 5 left: down to 0, short, never refused.
	short, err := w.s.OrderLines(ctx, w.env.Tenant, uuid.New(), "AD-2", lines[:1])
	if err != nil || len(short) != 1 {
		t.Fatalf("a pack too many: %v %v", short, err)
	}
	if got, _ := w.countOf(malt); got != 0 {
		t.Fatalf("after a pack too many: %d", got)
	}
}

// Receiving a product's line moves its stock item by the quantity times
// the uses the line was made with, at the line's cost over the uses (whole
// kobo); a line naming a stock item directly moves it by the quantity, at
// the line's cost; uses changed after the order was made do not change
// what it receives, and a product counted by none then is read when it
// comes.
func TestReceiveProductLineWithUses(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	p := w.purchases()
	malt := w.stockItem("Malt", "bottle", 0, map[string]int64{"pack": 6})
	corks := w.stockItem("Crown corks", "", 0, nil)
	var po stock.Purchase
	w.in(func(tx pgx.Tx) error {
		s, err := p.AddSupplierTx(ctx, tx, w.env.Tenant, "Nigerian Breweries", "0803 123 4500", "user:owner")
		if err != nil {
			return err
		}
		po, err = stock.DraftTx(ctx, tx, w.env.Tenant, stock.DraftIn{Supplier: s.ID, Actor: "user:owner", Key: "po-malt", Lines: []stock.PurchaseLine{
			{ItemID: "pack", Item: "Malt, pack of 6", Quantity: 3, CostKobo: 600005},
			{ItemID: corks.String(), Item: "Crown corks", Quantity: 50, CostKobo: 200},
			{ItemID: "crate", Item: "Malt, crate of 24", Quantity: 1, CostKobo: 2400000},
		}})
		return err
	})
	if l := po.Lines[0]; l.StockItemID == nil || *l.StockItemID != malt || l.Uses != 6 {
		t.Fatalf("the pack's line names its stock item: %+v", l)
	}
	if l := po.Lines[1]; l.StockItemID == nil || *l.StockItemID != corks || l.Uses != 1 {
		t.Fatalf("the corks' line: %+v", l)
	}
	if l := po.Lines[2]; l.StockItemID != nil || l.Uses != 0 {
		t.Fatalf("the crate's line, counted by none: %+v", l)
	}
	// The pack is counted 12 a sale from now: the order was made at 6.
	w.in(func(tx pgx.Tx) error {
		if err := productstock.LinkTx(ctx, tx, w.env.Tenant, "pack", malt, 12); err != nil {
			return err
		}
		// The crate, counted by none when the order was made, is counted
		// by the malt, 24 a sale, before it comes: what it moves is read
		// when it comes.
		return productstock.LinkTx(ctx, tx, w.env.Tenant, "crate", malt, 24)
	})
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE purchase_orders SET status = 'sent', approved_by = 'user:owner', sent_at = now() WHERE id = $1`, po.ID); err != nil {
		t.Fatal(err)
	}
	got, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"pack": 3, corks.String(): 50, "crate": 1}, "user:owner", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if n, cost := w.countOf(malt); n != 42 || cost == nil || *cost != 100000 {
		t.Fatalf("the malt: %d at %v; want 42 (3 packs of 6, a crate of 24) at 100000 (600005 / 6, 2400000 / 24)", n, cost)
	}
	if n, cost := w.countOf(corks); n != 50 || cost == nil || *cost != 200 {
		t.Fatalf("the corks: %d at %v", n, cost)
	}
	if got.Status != stock.FullReceived || got.ReceivedKobo != 3*600005+50*200+2400000 || got.Lines[0].Received != 3 ||
		got.Lines[2].StockItemID == nil || *got.Lines[2].StockItemID != malt || got.Lines[2].Uses != 24 {
		t.Fatalf("the order: %+v", got)
	}
	// The same receipt again moves nothing.
	if _, err := p.Receive(ctx, w.env.Tenant, po.ID, map[string]int64{"pack": 3}, "user:owner", "r1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.countOf(malt); n != 42 {
		t.Fatalf("received twice: %d", n)
	}
	// Not all came, as agreed: 1 pack of the 3 is taken off, 6 bottles.
	w.in(func(tx pgx.Tx) error {
		_, _, err := stock.AdjustShortTx(ctx, tx, w.env.Tenant, po.ID, []int64{2, 50, 1}, "user:owner", "short-1")
		return err
	})
	if n, _ := w.countOf(malt); n != 36 {
		t.Fatalf("after one pack short: %d, want 36", n)
	}
}
