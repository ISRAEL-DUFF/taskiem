package dispatch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// toDoor offers an order to a rider and takes it to the door.
func (w *world) toDoor(ref string) dispatch.Delivery {
	w.t.Helper()
	out, err := w.d.Offer(context.Background(), w.env.Tenant, ref, dispatch.OfferIn{})
	if err != nil {
		w.t.Fatal(err)
	}
	x := *out.Delivery
	w.must(x, dispatch.Accept)
	w.must(x, dispatch.PickedUp)
	w.must(x, dispatch.Arrived)
	return x
}

func (w *world) doneTexts(ref string) int {
	w.t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM platform_messages WHERE tenant_id = $1 AND dedup_key LIKE $2`,
		w.env.Tenant, "delivery:done:"+ref+"%").Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// The order is completed with the Delivered tap itself, and what follows
// the tap (the workflow told, the customer told) is carried through by a
// second tap when it failed the first time, once.
func TestASecondTapFinishesADelivery(t *testing.T) {
	w := newWorld(t)
	w.rider("Musa Bello", "+2348031110001", "Lekki")
	ref := w.order("Lekki", "none")
	x := w.toDoor(ref)
	told := w.d.Delivered
	fail := true
	w.d.Delivered = func(ctx context.Context, tenant uuid.UUID, ord storefront.Order) error {
		if fail {
			fail = false
			return errors.New("the database blipped")
		}
		return told(ctx, tenant, ord)
	}
	if _, err := w.press(x, dispatch.Delivered); err == nil {
		t.Fatal("the failure was not reported")
	}
	if o := w.get(ref); o.Status != "completed" {
		t.Fatalf("the order is %s after Delivered", o.Status)
	}
	w.must(x, dispatch.Delivered)
	if len(w.delivered) != 1 || w.doneTexts(ref) != 1 {
		t.Fatalf("after a second tap the workflow was told %d times and the customer %d", len(w.delivered), w.doneTexts(ref))
	}
	w.must(x, dispatch.Delivered)
	if err := w.d.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(w.delivered) != 1 || w.doneTexts(ref) != 1 {
		t.Fatalf("told again: the workflow %d times, the customer %d", len(w.delivered), w.doneTexts(ref))
	}
}

// A delivery left delivered with its order still open (the process died
// after the tap was saved) is finished by the sweep: the order completed,
// its workflow and its customer told, once.
func TestTheSweepFinishesADelivery(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.rider("Musa Bello", "+2348031110001", "Lekki")
	ref := w.order("Lekki", "none")
	x := w.toDoor(ref)
	// As the tap left it before this fix, when nothing ran after it.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE deliveries SET status = 'delivered', delivered_at = now() - interval '5 minutes' WHERE id = $1`, x.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := w.d.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if o := w.get(ref); o.Status != "completed" || len(w.delivered) != 1 || w.doneTexts(ref) != 1 {
			t.Fatalf("sweep %d: the order is %s, the workflow told %d times, the customer %d", i+1, o.Status, len(w.delivered), w.doneTexts(ref))
		}
	}
}

// Cash at the door, with what follows the tap failing: the sweep carries
// it through, and the order is paid and completed.
func TestTheSweepFinishesACashDelivery(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.rider("Musa Bello", "+2348031110001", "Lekki")
	ref := w.order("Lekki", "on_delivery")
	x := w.toDoor(ref)
	fail := true
	told := w.d.Delivered
	w.d.Delivered = func(ctx context.Context, tenant uuid.UUID, ord storefront.Order) error {
		if fail {
			fail = false
			return errors.New("the database blipped")
		}
		return told(ctx, tenant, ord)
	}
	_, _ = w.press(x, dispatch.Cash)
	if o := w.get(ref); o.Status != "completed" || o.PaidAt == nil {
		t.Fatalf("after Cash the order is %s, paid %v", o.Status, o.PaidAt)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE deliveries SET delivered_at = now() - interval '5 minutes' WHERE id = $1`, x.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.delivered) != 1 || w.doneTexts(ref) != 1 {
		t.Fatalf("the workflow was told %d times and the customer %d", len(w.delivered), w.doneTexts(ref))
	}
}
