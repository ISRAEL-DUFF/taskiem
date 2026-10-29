package stock_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
)

// An item on two lines of a sale ("2 rice, 3 rice") moves by both: one
// movement for the item, of the lines' sum.
func TestSaleOfAnItemOnTwoLines(t *testing.T) {
	w := newWorld(t, map[string]int64{"rice": 0})
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(10)}).ID)
	x := w.sale(stock.SaleIn{PaidHow: stock.Cash, Lines: []stock.SaleLine{
		{ItemID: "rice", Item: "Rice", Quantity: 2, PriceKobo: 4500000},
		{ItemID: "rice", Item: "Rice", Quantity: 3, PriceKobo: 4400000},
	}})
	if x.AmountKobo != 2*4500000+3*4400000 || len(x.Lines) != 2 {
		t.Fatalf("sale: %+v", x)
	}
	if w.tb.count("rice") != 5 || w.ledger("rice") != 5 {
		t.Fatalf("rice: %d in the table, %d by its movements", w.tb.count("rice"), w.ledger("rice"))
	}
	if m := w.movements("rice"); len(m) != 2 || *m[0].Change != -5 {
		t.Fatalf("movements: %+v", m)
	}
}

// refusedOnce refuses its first change as another writer's would, and
// keeps that answer under its key; the count is back where it was by the
// time it is read again.
type refusedOnce struct {
	*table
	done bool
}

func (f *refusedOnce) Change(ctx context.Context, tenant uuid.UUID, row string, from, to int64, key string) (bool, error) {
	if !f.done {
		f.done = true
		f.mu.Lock()
		f.keys[key] = false
		f.mu.Unlock()
		return false, nil
	}
	return f.table.Change(ctx, tenant, row, from, to, key)
}

// A change refused at a count, read again at the same count, is a new
// change: it lands, rather than being answered from the refusal.
func TestChangeRefusedThenTheCountCameBack(t *testing.T) {
	w := newWorld(t, map[string]int64{"rice": 0})
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(10)}).ID)
	w.s.Table = &refusedOnce{table: w.tb}
	m := w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Adjustment, Delta: n(-1)}).ID)
	if m.Status != "done" || w.tb.count("rice") != 9 {
		t.Fatalf("movement: %+v, rice %d", m, w.tb.count("rice"))
	}
}

// A caller's key of any length the API takes is kept, hashed when long:
// sales, counts and movements made under it, and the same key again is
// the same one.
func TestLongKeys(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0})
	long := "api-" + strings.Repeat("k", 251)
	mv := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(10), Key: long})
	if again := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(10), Key: long}); again.ID != mv.ID {
		t.Fatalf("the same key: %s, %s", mv.ID, again.ID)
	}
	w.apply(mv.ID)
	x := w.sale(stock.SaleIn{PaidHow: stock.Cash, Key: long, Lines: []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 1, PriceKobo: 100}}})
	if again := w.sale(stock.SaleIn{PaidHow: stock.Cash, Key: long, Lines: []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 1, PriceKobo: 100}}}); again.ID != x.ID {
		t.Fatalf("the same sale: %s, %s", x.ID, again.ID)
	}
	var c stock.StockCount
	w.in(func(tx pgx.Tx) error {
		var err error
		c, _, err = stock.CountTx(ctx, tx, w.env.Tenant, stock.CountIn{Lines: []stock.CountLine{{ItemID: "rice", Item: "Rice", Expected: n(9), Counted: 7}}, Actor: "user:owner", Key: long})
		return err
	})
	if c.Changed != 1 {
		t.Fatalf("count: %+v", c)
	}
	if got := stock.FitKey("short"); got != "short" {
		t.Errorf("a short key: %q", got)
	}
	if got := stock.FitKey(long); len(got) > 64 || got != stock.FitKey(long) {
		t.Errorf("a long key: %q", got)
	}
}
