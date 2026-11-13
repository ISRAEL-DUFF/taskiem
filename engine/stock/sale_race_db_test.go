package stock_test

import (
	"context"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/stock"
)

// The same sale sent twice at the same moment (a double tap, a retry that
// overlaps) is one sale, and both callers are answered with it.
func TestTheSameSaleAtOnceIsOneSale(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, map[string]int64{"rice": 0})
	w.apply(w.record(stock.Move{ItemID: "rice", Item: "Rice", Reason: stock.Received, Delta: n(100)}).ID)
	for round := 0; round < 5; round++ {
		in := stock.SaleIn{PaidHow: stock.Cash, Actor: "user:owner", Key: "double-" + string(rune('a'+round)),
			Lines: []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 2, PriceKobo: 4500000}}}
		const callers = 6
		var wg sync.WaitGroup
		got := make([]stock.CounterSale, callers)
		errs := make([]error, callers)
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got[i], errs[i] = w.s.RecordSale(ctx, w.env.Tenant, in, w.log())
			}()
		}
		wg.Wait()
		fresh := 0
		for i := range got {
			if errs[i] != nil {
				t.Fatalf("round %d, caller %d: %v", round, i, errs[i])
			}
			if got[i].ID != got[0].ID {
				t.Fatalf("round %d: two sales, %s and %s", round, got[0].Reference, got[i].Reference)
			}
			if !got[i].Replayed {
				fresh++
			}
		}
		if fresh != 1 {
			t.Fatalf("round %d: %d callers were told the sale was new", round, fresh)
		}
	}
	if err := w.s.ApplyPending(ctx, w.env.Tenant); err != nil {
		t.Fatal(err)
	}
	if got := w.tb.count("rice"); got != 90 {
		t.Fatalf("rice is %d after five sales of 2", got)
	}
}
