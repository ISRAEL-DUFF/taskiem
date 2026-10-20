package storefront_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// fakeStock is a catalogue table's stock column with Xenibase's
// Idempotency-Key replay: a key sent again answers as it first did.
type fakeStock struct {
	mu      sync.Mutex
	n       map[string]int64
	keys    map[string]bool
	changes int
	// race moves the stock once, between the first read and change, as
	// another order accepted at the same moment would.
	race int64
}

func (f *fakeStock) Read(_ context.Context, _ uuid.UUID, row string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.n[row]
	return n, ok, nil
}

func (f *fakeStock) Change(_ context.Context, _ uuid.UUID, row string, from, to int64, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if applied, ok := f.keys[key]; ok {
		return applied, nil
	}
	if f.race != 0 {
		f.n[row] -= f.race
		f.race = 0
	}
	applied := f.n[row] == from
	if applied {
		f.n[row] = to
		f.changes++
	}
	f.keys[key] = applied
	return applied, nil
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

func TestAcceptLowersStock(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	st := &fakeStock{n: map[string]int64{"1": 6}, keys: map[string]bool{}, race: 1}
	w.orders.Stock = st
	six := int64(6)
	w.in(func(tx pgx.Tx) error {
		return storefront.SyncCatalogue(ctx, tx, w.env.Tenant, []storefront.Item{{ID: "1", Name: "Jollof rice", Available: true, Stock: &six}})
	})

	// Accepting two while another order takes one: the change is refused
	// at 6, read again at 5, and lands at 3, in the table and the copy.
	ref := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.decide(ref, "decide", "approved")
	if o := w.order(ref); o.Status != "awaiting_payment" {
		t.Fatalf("after accepting: %+v", o)
	}
	if st.n["1"] != 3 || st.changes != 1 || w.copiedStock("1") != 3 {
		t.Fatalf("stock %d after %d changes, copy %d; want 3 after 1", st.n["1"], st.changes, w.copiedStock("1"))
	}

	// A retried accept changes nothing more.
	out, err := w.orders.Decide(ctx, w.env.Tenant, ref, "accept", "", "test")
	if err != nil || st.n["1"] != 3 || st.changes != 1 || len(out["short"].([]string)) != 0 {
		t.Fatalf("retry: %v %v, stock %d", out, err, st.n["1"])
	}

	// A change sent but not recorded as done (the step died in between) is
	// sent again with the same key and replays: it still lands once.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET stock = '{"1": {"from": 5, "to": 3, "done": false}}' WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := w.orders.Decide(ctx, w.env.Tenant, ref, "accept", "", "test"); err != nil || st.n["1"] != 3 || st.changes != 1 {
		t.Fatalf("replay: %v, stock %d after %d changes", err, st.n["1"], st.changes)
	}

	// Accepting more than is left lowers it to zero and says so.
	st.n["1"] = 1
	ref2 := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.decide(ref2, "decide", "approved")
	o := w.order(ref2)
	if st.n["1"] != 0 || o.Note != "Accepted with too little stock of Jollof rice" || w.copiedStock("1") != 0 {
		t.Fatalf("short: stock %d, note %q, copy %d", st.n["1"], o.Note, w.copiedStock("1"))
	}

	// A declined order leaves the stock alone.
	st.n["1"] = 9
	ref3 := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.decide(ref3, "decide", "rejected")
	if st.n["1"] != 9 {
		t.Fatalf("a declined order took stock: %d", st.n["1"])
	}
}
