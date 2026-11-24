package storefront_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/stock"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// stockWorld is a world whose product "1" is counted by a stock item: 6.
func stockWorld(t *testing.T) (*world, *shelf) {
	w := newWorld(t)
	st := newShelf(w, map[string]int64{"1": 6})
	six := int64(6)
	w.in(func(tx pgx.Tx) error {
		return storefront.SyncCatalogue(context.Background(), tx, w.env.Tenant, []storefront.Item{{ID: "1", Name: "Jollof rice", Available: true, Stock: &six}})
	})
	return w, st
}

// accepted is an order of 2 of item "1", accepted: 4 are left.
func (w *world) accepted(st *shelf) string {
	w.t.Helper()
	ref := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(context.Background()); err != nil {
		w.t.Fatal(err)
	}
	w.env.Drain(w.t)
	before := st.count("1")
	w.decide(ref, "decide", "approved")
	if st.count("1") != before-2 {
		w.t.Fatalf("accepting %s left %d of %d", ref, st.count("1"), before)
	}
	// The order's workflow goes on to ask for payment; these tests end it
	// while it is accepted.
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE storefront_orders SET status = 'accepted', expires_at = NULL WHERE reference = $1`, ref); err != nil {
		w.t.Fatal(err)
	}
	return ref
}

func (w *world) orderMoves(ref string) (back int, sum int64) {
	w.t.Helper()
	w.in(func(tx pgx.Tx) error {
		list, err := stock.ListTx(context.Background(), tx, w.env.Tenant, stock.Filter{ItemID: "1", Reason: stock.Order})
		for _, m := range list {
			if m.Reference != ref {
				continue
			}
			sum += *m.Change
			if *m.Change > 0 {
				back++
			}
		}
		return err
	})
	return back, sum
}

// The stock an accepted order took is put back when the order is
// declined, cancelled, left refund due or not paid for in time: once,
// however often the step is retried, and never once its goods have left.
func TestStockComesBackWhenAnAcceptedOrderEnds(t *testing.T) {
	ctx := context.Background()
	w, st := stockWorld(t)
	check := func(name, ref string, want int64) {
		t.Helper()
		back, sum := w.orderMoves(ref)
		wantBack, wantSum := 1, int64(0)
		if want == 4 {
			wantBack, wantSum = 0, -2
		}
		if st.count("1") != want || back != wantBack || sum != wantSum || w.copiedStock("1") != want {
			t.Fatalf("%s: stock %d (the page %d), %d put back, the order's movements add up to %d; want %d, %d and %d",
				name, st.count("1"), w.copiedStock("1"), back, sum, want, wantBack, wantSum)
		}
	}
	for _, decision := range []string{"decline", "cancel"} {
		ref := w.accepted(st)
		for i := 0; i < 2; i++ {
			if _, err := w.orders.Decide(ctx, w.env.Tenant, ref, decision, "", "test"); err != nil {
				t.Fatalf("%s: %v", decision, err)
			}
			check(decision, ref, 6)
		}
	}

	// Paid, then declined: refund due.
	ref := w.accepted(st)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'paid', paid_at = now() WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, err := w.orders.Decide(ctx, w.env.Tenant, ref, "decline", "", "test")
		if err != nil || out["status"] != "refund_due" {
			t.Fatalf("refund due: %v %v", out, err)
		}
		check("refund due", ref, 6)
	}

	// Accepted, asked to pay, and not paid in time.
	ref = w.accepted(st)
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'awaiting_payment', expires_at = now() - interval '1 hour' WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := w.orders.CheckPayments(ctx); err != nil {
			t.Fatal(err)
		}
		if o := w.order(ref); o.Status != "expired" {
			t.Fatalf("status %s", o.Status)
		}
		check("expired", ref, 6)
	}

	// Its goods left with the rider and were delivered: nothing comes back.
	ref = w.accepted(st)
	rider := uuid.New()
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO riders (id, tenant_id, phone_hash, phone, name, created_by) VALUES ($1, $2, $3, '{}', '{}', 'user:ada')`,
		rider, w.env.Tenant, []byte(rider.String())[:32]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO deliveries (id, tenant_id, order_id, rider_id, status, offered_by, expires_at, delivered_at)
		SELECT $1, tenant_id, id, $2, 'delivered', 'user:ada', now(), now() FROM storefront_orders WHERE reference = $3`, uuid.New(), rider, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'arrived' WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := w.orders.Decide(ctx, w.env.Tenant, ref, "cancel", "", "test"); err != nil {
			t.Fatal(err)
		}
		check("delivered", ref, 4)
	}
}
