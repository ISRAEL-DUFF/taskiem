package storefront_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/productstock"
	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// shelf is the world's products counted by stock items of their own
// (what-a-business-buys §4): accepting an order lowers them as movements.
type shelf struct {
	w     *world
	items map[string]uuid.UUID
}

// newShelf counts each product of counts by a stock item, received at its
// count, and lowers orders' stock through engine/stock.
func newShelf(w *world, counts map[string]int64) *shelf {
	w.t.Helper()
	sh := &shelf{w: w, items: map[string]uuid.UUID{}}
	ctx := context.Background()
	w.in(func(tx pgx.Tx) error {
		for row, n := range counts {
			x, err := stock.CreateItemTx(ctx, tx, w.env.Tenant, stock.NewItem{Name: "Item " + row, Actor: "user:ada"})
			if err != nil {
				return err
			}
			sh.items[row] = x.ID
			if err := productstock.LinkTx(ctx, tx, w.env.Tenant, row, x.ID); err != nil {
				return err
			}
			if n > 0 {
				if _, err := stock.RecordTx(ctx, tx, w.env.Tenant, stock.Move{ItemID: row, Item: x.Name, Reason: stock.Received, Delta: &n,
					Actor: "user:ada", Key: "seed-" + row}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	w.orders.Moves = &stock.Stock{Pool: w.env.DB.App}
	return sh
}

// count is a product's count: its stock item's.
func (sh *shelf) count(row string) int64 {
	sh.w.t.Helper()
	var n int64
	sh.w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT count FROM stock_items WHERE id = $1`, sh.items[row]).Scan(&n)
	})
	return n
}

// set counts a product's stock item at n, as a count movement.
func (sh *shelf) set(row string, n int64) {
	sh.w.t.Helper()
	sh.w.in(func(tx pgx.Tx) error {
		_, err := stock.RecordTx(context.Background(), tx, sh.w.env.Tenant, stock.Move{ItemID: row, Item: "Item " + row, Reason: stock.Count, SetTo: &n,
			Actor: "user:ada", Key: uuid.NewString()})
		return err
	})
}

func (w *world) copiedStock(row string) int64 {
	w.t.Helper()
	var n int64
	w.in(func(tx pgx.Tx) error {
		items, err := storefront.Catalogue(context.Background(), tx, w.env.Tenant)
		for _, it := range items {
			if it.ID == row && it.Stock != nil {
				n = *it.Stock
			}
		}
		return err
	})
	return n
}

// Accepting an order lowers its product's stock item once, as an order
// movement, in the page's copy too; more than is left lowers it to zero
// and says so; a declined order leaves it alone.
func TestAcceptLowersStock(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	st := newShelf(w, map[string]int64{"1": 6})
	six := int64(6)
	w.in(func(tx pgx.Tx) error {
		return storefront.SyncCatalogue(ctx, tx, w.env.Tenant, []storefront.Item{{ID: "1", Name: "Jollof rice", Available: true, Stock: &six}})
	})
	ref := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.decide(ref, "decide", "approved")
	if o := w.order(ref); o.Status != "awaiting_payment" {
		t.Fatalf("after accepting: %+v", o)
	}
	if st.count("1") != 4 || w.copiedStock("1") != 4 {
		t.Fatalf("stock %d, copy %d; want 4", st.count("1"), w.copiedStock("1"))
	}
	// Retried: nothing moves twice.
	if out, err := w.orders.Decide(ctx, w.env.Tenant, ref, "accept", "", "test"); err != nil || st.count("1") != 4 || len(out["short"].([]string)) != 0 {
		t.Fatalf("retry: %v %v, stock %d", out, err, st.count("1"))
	}
	var list []stock.Movement
	w.in(func(tx pgx.Tx) error {
		var err error
		list, err = stock.ListTx(ctx, tx, w.env.Tenant, stock.Filter{ItemID: "1", Reason: stock.Order})
		return err
	})
	if len(list) != 1 || list[0].Reference != ref || *list[0].Change != -2 || list[0].ItemID != st.items["1"].String() {
		t.Fatalf("movements: %+v", list)
	}

	// Accepting more than is left lowers it to zero and says so.
	st.set("1", 1)
	ref2 := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.decide(ref2, "decide", "approved")
	o := w.order(ref2)
	if st.count("1") != 0 || o.Note != "Accepted with too little stock of Jollof rice" || w.copiedStock("1") != 0 {
		t.Fatalf("short: stock %d, note %q, copy %d", st.count("1"), o.Note, w.copiedStock("1"))
	}

	// A declined order leaves the stock alone.
	st.set("1", 9)
	ref3 := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.decide(ref3, "decide", "rejected")
	if st.count("1") != 9 {
		t.Fatalf("a declined order took stock: %d", st.count("1"))
	}
}

// The catalogue copy reads how many are left of each product from the
// stock item it is counted by, and the page shows it sold out from there;
// the catalogue table holds no count, and a front's own list none.
func TestCatalogueCountsFromStockItems(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	st := newShelf(w, map[string]int64{"1": 0, "2": 3})
	rows := []map[string]any{{"id": "1", "name": "Jollof rice", "stock": 99}, {"id": "2", "name": "Chapman"}, {"id": "3", "name": "Delivery"}}
	m := storefront.Columns{Name: "name"}
	w.in(func(tx pgx.Tx) error {
		items, err := storefront.CatalogueItems(ctx, tx, w.env.Tenant, rows, m)
		if err != nil {
			return err
		}
		if len(items) != 3 || items[0].Stock == nil || *items[0].Stock != 0 || !items[0].SoldOut() || *items[1].Stock != 3 || items[1].SoldOut() ||
			items[2].Stock != nil || items[2].SoldOut() {
			t.Errorf("the catalogue's items: %+v", items)
		}
		list, err := storefront.Items(ctx, tx, w.env.Tenant, rows, m)
		if err != nil {
			return err
		}
		if list[0].Stock != nil || list[1].Stock != nil {
			t.Errorf("a front's own list counted: %+v", list)
		}
		return nil
	})
	// A count changed by a movement reaches the copy at once.
	w.in(func(tx pgx.Tx) error {
		items, err := storefront.CatalogueItems(ctx, tx, w.env.Tenant, rows, m)
		if err != nil {
			return err
		}
		return storefront.SyncCatalogue(ctx, tx, w.env.Tenant, items)
	})
	st.set("1", 5)
	if w.copiedStock("1") != 5 {
		t.Fatalf("copy %d after a count of 5", w.copiedStock("1"))
	}
}

// A size's new count goes into its product on the catalogue's copy only:
// a front's list from another table that carries a size of the same row
// id keeps its own. The product's count is the sizes' together, unknown
// when one keeps no count.
func TestCopySizeCountCatalogueOnly(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	n := func(v int64) *int64 { return &v }
	product := func(m *int64) storefront.Item {
		return storefront.Item{ID: "1", Name: "Tee", Available: true, Stock: n(5), Variants: 2,
			Sizes: []storefront.Size{{ID: "2", Variant: map[string]string{"Size": "S"}, Stock: n(3)}, {ID: "3", Variant: map[string]string{"Size": "M"}, Stock: m}}}
	}
	w.in(func(tx pgx.Tx) error {
		if err := storefront.SyncCatalogue(ctx, tx, w.env.Tenant, []storefront.Item{product(n(2))}); err != nil {
			return err
		}
		if err := storefront.SyncList(ctx, tx, w.env.Tenant, "other", []storefront.Item{product(n(2))}); err != nil {
			return err
		}
		return storefront.CopySizeCount(ctx, tx, w.env.Tenant, "2", 1)
	})
	if got := w.copiedStock("1"); got != 3 {
		t.Errorf("the catalogue's product: %d, want 3", got)
	}
	w.in(func(tx pgx.Tx) error {
		items, err := storefront.ListItems(ctx, tx, w.env.Tenant, "other")
		if err != nil {
			return err
		}
		if len(items) != 1 || *items[0].Stock != 5 || *items[0].Sizes[0].Stock != 3 {
			t.Errorf("the other table's list was changed: %+v", items)
		}
		return nil
	})
	// One size keeps no count: the product's count is unknown.
	w.in(func(tx pgx.Tx) error {
		if err := storefront.SyncCatalogue(ctx, tx, w.env.Tenant, []storefront.Item{product(nil)}); err != nil {
			return err
		}
		if err := storefront.CopySizeCount(ctx, tx, w.env.Tenant, "2", 0); err != nil {
			return err
		}
		items, err := storefront.Catalogue(ctx, tx, w.env.Tenant)
		if err != nil {
			return err
		}
		if len(items) != 1 || items[0].Stock != nil || items[0].SoldOut() {
			t.Errorf("a product with an uncounted size: %+v", items)
		}
		return nil
	})
}
