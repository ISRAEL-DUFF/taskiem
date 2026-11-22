package stock_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// The Stock page's rows are stock items (what a business buys §4.5): one
// counting two products lists both with their uses; a material lists none;
// the filter picks sold or not sold; a retired one shows only when asked.
func TestStockItemRows(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, nil)
	malt := w.stockItem("Malt", "bottle", 20, map[string]int64{"pack": 6, "single": 1})
	flour := w.stockItem("Flour", "bag", 3, nil)
	old := w.stockItem("Old wrap", "", 0, nil)
	w.in(func(tx pgx.Tx) error {
		cost, low := int64(5000), int64(5)
		if _, err := stock.SetItemTx(ctx, tx, w.env.Tenant, flour, stock.ItemSet{CostKobo: &cost, Low: &low}); err != nil {
			return err
		}
		return stock.RetireItemTx(ctx, tx, w.env.Tenant, old, false)
	})
	rows := []map[string]any{
		{"id": "pack", "name": "Malt, pack of 6", "price": 3000.0},
		{"id": "single", "name": "Malt", "price": 500.0},
	}
	m := storefront.Columns{Name: "name", Price: "price"}
	list := func(show stock.Show, retired bool) []stock.StockItemRow {
		t.Helper()
		var out []stock.StockItemRow
		w.in(func(tx pgx.Tx) error {
			var err error
			out, err = stock.StockItemRowsTx(ctx, tx, w.env.Tenant, rows, m, show, retired)
			return err
		})
		return out
	}

	all := list(stock.ShowAll, false)
	if len(all) != 2 || all[0].ID != flour || all[1].ID != malt {
		t.Fatalf("all: %+v", all)
	}
	f, mt := all[0], all[1]
	if len(f.SoldAs) != 0 || !f.Low || f.Suggest == nil || *f.Suggest != 7 || f.ValueKobo != 15000 || f.Count != 3 {
		t.Fatalf("flour, a material: %+v", f)
	}
	if mt.Count != 20 || mt.Low || mt.Suggest != nil || len(mt.SoldAs) != 2 ||
		mt.SoldAs[0].ProductID != "single" || mt.SoldAs[0].Uses != 1 || mt.SoldAs[1].ProductID != "pack" || mt.SoldAs[1].Uses != 6 ||
		mt.SoldAs[1].PriceKobo == nil || *mt.SoldAs[1].PriceKobo != 300000 || !mt.SoldAs[1].Available {
		t.Fatalf("malt, sold as two: %+v", mt)
	}
	if s := list(stock.ShowSold, false); len(s) != 1 || s[0].ID != malt {
		t.Fatalf("sold: %+v", s)
	}
	if s := list(stock.ShowNotSold, false); len(s) != 1 || s[0].ID != flour {
		t.Fatalf("not sold: %+v", s)
	}
	if s := list(stock.ShowNotSold, true); len(s) != 2 || s[1].ID != old || !s[1].Retired {
		t.Fatalf("not sold, retired too: %+v", s)
	}
}
