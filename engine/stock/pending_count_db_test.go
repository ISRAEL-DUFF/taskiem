package stock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
)

// A count stands: a sale made before it that had not reached the table
// lands first, as a sale, so the counted number is what the table holds
// afterwards and the count books no loss for goods that were sold.
func TestCountStandsOverAnOlderSaleStillOnItsWay(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0})
	cost := int64(4000000)
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(10), CostKobo: &cost}).ID)

	// The sale of 2 cannot reach the table.
	w.tb.fail = errors.New("the project is down")
	sale := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(-2), CostKobo: &cost})
	if m, err := w.s.Apply(ctx, w.env.Tenant, sale.ID); err == nil || m.Status != "pending" {
		t.Fatalf("sale: %+v %v", m, err)
	}
	w.tb.fail = nil

	// The clerk counts the shelf: 8 are there; the screen still shows 10.
	c, err := w.s.RecordCount(ctx, w.env.Tenant, stock.CountIn{Reason: "weekly", Actor: "user:owner", Key: "c-1", Lines: []stock.CountLine{
		{ItemID: "rice", Item: "Rice", Expected: n(10), Counted: 8, CostKobo: &cost},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if c.LostKobo != 0 || c.Lines[0].Lost() != 0 {
		t.Errorf("the count books a loss for what was sold: %+v", c)
	}
	// The sweep, once the sale is due again.
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE stock_movements SET claimed_at = now() - interval '2 hours' WHERE status = 'pending'`)
		return err
	})
	if err := w.s.ApplyPending(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	if got := w.tb.count("rice"); got != 8 || w.ledger("rice") != 8 {
		t.Fatalf("rice is %d in the table and %d by its movements, counted 8", got, w.ledger("rice"))
	}
	for _, m := range w.movements("rice") {
		if m.Status != "done" {
			t.Errorf("still on its way: %+v", m)
		}
	}
	var sh stock.Shrink
	w.in(func(tx pgx.Tx) error {
		var err error
		sh, err = stock.ShrinkTx(ctx, tx, w.env.Tenant, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
		return err
	})
	if sh.Lost != 0 || sh.LostKobo != 0 || sh.Sold != 2 {
		t.Fatalf("shrinkage: sold %d, lost %d (%d kobo); want sold 2, lost 0", sh.Sold, sh.Lost, sh.LostKobo)
	}
}

// With the table still down when the shelf is counted, the count waits
// behind the older sale rather than landing before it.
func TestCountWaitsBehindAnOlderMovement(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0})
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(10)}).ID)
	w.tb.fail = errors.New("the project is down")
	sale := w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Sale, Delta: n(-2)})
	_, _ = w.s.Apply(ctx, w.env.Tenant, sale.ID)
	// 7 on the shelf: one is missing besides the two sold.
	if _, err := w.s.RecordCount(ctx, w.env.Tenant, stock.CountIn{Actor: "user:owner", Key: "c-1", Lines: []stock.CountLine{
		{ItemID: "rice", Item: "Rice", Expected: n(10), Counted: 7},
	}}); err != nil {
		t.Fatal(err)
	}
	if w.tb.count("rice") != 10 {
		t.Fatalf("rice %d while the table is down", w.tb.count("rice"))
	}
	w.tb.fail = nil
	// Only the count is due: it still lands after the sale.
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE stock_movements SET claimed_at = now() - interval '2 hours' WHERE status = 'pending' AND reason = 'count'`)
		return err
	})
	if err := w.s.ApplyPending(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	if got := w.tb.count("rice"); got != 7 || w.ledger("rice") != 7 {
		t.Fatalf("rice is %d in the table and %d by its movements, counted 7", got, w.ledger("rice"))
	}
	var sold, lost int64
	for _, m := range w.movements("rice") {
		if m.Change == nil {
			t.Fatalf("still on its way: %+v", m)
		}
		switch m.Reason {
		case stock.Sale:
			sold -= *m.Change
		case stock.Count:
			lost -= *m.Change
		}
	}
	if sold != 2 || lost != 1 {
		t.Fatalf("sold %d, lost %d; want 2 and 1", sold, lost)
	}
}
