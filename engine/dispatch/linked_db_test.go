package dispatch_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/links"
	"github.com/israel-duff/taskiem/engine/riders"
)

// riderBusiness makes a business on Taskiem for a rider, linked with the
// world's business (the rider supplies it), and answers it and the link.
func (w *world) riderBusiness() (uuid.UUID, uuid.UUID) {
	w.t.Helper()
	ctx := context.Background()
	rider := w.env.AddTenant(w.t)
	by := links.Actor{Type: "user", ID: "ada"}
	on := true
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{rider}, func(tx pgx.Tx) error {
		_, err := links.SetFindableTx(ctx, tx, rider, &on, "user:musa")
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := links.RequestTx(ctx, tx, rider, "supplier", by)
		return err
	})
	var link uuid.UUID
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{rider}, func(tx pgx.Tx) error {
		list, _, err := links.ListTx(ctx, tx)
		if err != nil || len(list) != 1 {
			w.t.Fatalf("the rider's links: %v %v", list, err)
		}
		link = list[0].ID
		_, err = links.AnswerTx(ctx, tx, link, links.Accept, links.Actor{Type: "user", ID: "musa"})
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	return rider, link
}

// riderOrders are the rider business's linked orders, open ones first.
func (w *world) riderOrders(rider uuid.UUID) []links.Order {
	w.t.Helper()
	var out []links.Order
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{rider}, func(tx pgx.Tx) error {
		var err error
		out, err = links.ListOrdersTx(context.Background(), tx, false, 50)
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
	return out
}

// cards are a conversation's cards on one side, oldest first: "doc:event:mine".
func (w *world) cards(tenant, link uuid.UUID) []string {
	w.t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT (card ->> 'doc') || ':' || (card ->> 'event') || ':' || mine::text
		FROM link_messages WHERE tenant_id = $1 AND link_id = $2 AND kind = 'card' ORDER BY created_at, id`, tenant, link)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A rider that is a business on Taskiem (docs/spec/chat-riders.md, phase
// 1): an offer reaches them as an order in their own business beside
// their chat app's message, with the customer sealed and a card on both
// sides; their steps from their own business and from their buttons
// alike move the business's delivery and their order together; the
// customer's details are theirs while they hold it and gone once it is
// delivered; an offer that runs out and a release cancel their order.
func TestLinkedRiderOrder(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	rider, link := w.riderBusiness()
	musa := w.rider("Musa", "+2348031110011", "Lekki")
	lid := link.String()
	w.in(func(tx pgx.Tx) error {
		_, err := w.riders.UpdateTx(ctx, tx, w.env.Tenant, musa.ID, riders.In{LinkID: &lid}, "user:ada")
		return err
	})
	if x, _ := w.riders.Get(ctx, w.env.Tenant, musa.ID); x.LinkID == nil || *x.LinkID != link {
		t.Fatalf("the rider's link: %+v", x.LinkID)
	}

	// The offer: the business's delivery as before, and the rider's order.
	ref := w.order("Lekki", "none")
	out, err := w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{})
	if err != nil || out.Status != "offered" || out.Delivery.LinkOrderID == nil {
		t.Fatalf("offered %+v %v", out, err)
	}
	x := *out.Delivery
	if got := w.stages(x.ID); len(got) != 1 || got[0] != dispatch.StageOffer {
		t.Fatalf("the chat app's offer still goes: %v", got)
	}
	orders := w.riderOrders(rider)
	if len(orders) != 1 || orders[0].ID != *x.LinkOrderID || orders[0].Delivery == nil || orders[0].Status != links.OrderReceived {
		t.Fatalf("the rider's orders: %+v", orders)
	}
	o := orders[0]
	if o.Delivery.DeliveryID != x.ID || o.Reference != ref || o.TotalKobo != musa.FeeKobo || o.Lines[0].Item != "Delivery to Lekki" ||
		o.Delivery.DropArea != "Lekki" || o.Delivery.Pickup != "4 Akoka Road, Yaba" || o.Delivery.ExpiresAt == nil || o.Held() {
		t.Fatalf("the rider's order: %+v %+v", o, o.Delivery)
	}
	want := []string{"dispatch:offered:true"}
	if got := w.cards(w.env.Tenant, link); !equal(got, want) {
		t.Fatalf("the business's cards: %v", got)
	}
	if got := w.cards(rider, link); !equal(got, []string{"delivery:offered:false"}) {
		t.Fatalf("the rider's cards: %v", got)
	}

	// Accepted from the rider's own business: the delivery is held, the
	// customer's details are theirs now.
	tap, _, err := w.d.RiderStep(ctx, rider, o.ID.String(), dispatch.Accept)
	if err != nil || !tap.Moved {
		t.Fatalf("accept from the rider's business: %+v %v", tap, err)
	}
	if h := w.held(ref); h.Status != "accepted" || h.ID != x.ID {
		t.Fatalf("held: %+v", h)
	}
	o = w.riderOrders(rider)[0]
	if o.Status != links.OrderAccepted || !o.Held() {
		t.Fatalf("the rider's order after accepting: %s", o.Status)
	}
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{rider}, func(tx pgx.Tx) error {
		c, err := links.OpenDeliveryCustomerTx(ctx, tx, w.env.Vault, rider, o.ID)
		if err != nil {
			return err
		}
		if c.Name != "Bola Ade" || c.Address != "12 Admiralty Way" || c.Landmark != "opposite the Shoprite gate" || c.Phone == "" {
			t.Fatalf("the customer while held: %+v", c)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Accepting again changes nothing, and posts no second card.
	if again, _, err := w.d.RiderStep(ctx, rider, o.ID.String(), dispatch.Accept); err != nil || again.Moved {
		t.Fatalf("accepted twice: %+v %v", again, err)
	}

	// Picked up from the button (the business's side): the rider's order
	// follows.
	w.must(x, dispatch.PickedUp)
	o = w.riderOrders(rider)[0]
	if o.Status != links.OrderDispatched || o.Delivery.PickedUpAt == nil {
		t.Fatalf("picked up: %s %+v", o.Status, o.Delivery)
	}
	// Arrived from the rider's business; delivered too.
	if _, _, err := w.d.RiderStep(ctx, rider, o.ID.String(), dispatch.Arrived); err != nil {
		t.Fatal(err)
	}
	if o = w.riderOrders(rider)[0]; o.Status != links.OrderArrived || o.Delivery.ArrivedAt == nil {
		t.Fatalf("arrived: %s", o.Status)
	}
	if _, _, err := w.d.RiderStep(ctx, rider, o.ID.String(), dispatch.Delivered); err != nil {
		t.Fatal(err)
	}
	if got := w.get(ref); got.Status != "completed" {
		t.Fatalf("the business's order: %s", got.Status)
	}
	if h := w.held(ref); h.Status != "delivered" {
		t.Fatalf("the delivery: %s", h.Status)
	}
	o = w.riderOrders(rider)[0]
	if o.Status != links.OrderDelivered || o.Held() || o.Lines[0].Delivered == nil || *o.Lines[0].Delivered != 1 {
		t.Fatalf("delivered: %s %+v", o.Status, o.Lines)
	}
	// The customer's details left with the delivery; the name stays.
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{rider}, func(tx pgx.Tx) error {
		c, err := links.OpenDeliveryCustomerTx(ctx, tx, w.env.Vault, rider, o.ID)
		if err != nil {
			return err
		}
		if c.Name != "Bola Ade" || c.Address != "" || c.Phone != "" {
			t.Fatalf("the customer after delivery: %+v", c)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want = []string{"dispatch:offered:true", "dispatch:accepted:false", "dispatch:picked_up:false", "dispatch:arrived:false", "dispatch:delivered:false"}
	if got := w.cards(w.env.Tenant, link); !equal(got, want) {
		t.Fatalf("the business's cards: %v", got)
	}
	want = []string{"delivery:offered:false", "delivery:accepted:true", "delivery:picked_up:true", "delivery:arrived:true", "delivery:delivered:true"}
	if got := w.cards(rider, link); !equal(got, want) {
		t.Fatalf("the rider's cards: %v", got)
	}

	// An offer that runs out: the rider's order is cancelled.
	ref = w.order("Lekki", "none")
	out, err = w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{})
	if err != nil || out.Delivery.LinkOrderID == nil {
		t.Fatalf("second offer: %+v %v", out, err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE deliveries SET expires_at = now() - interval '1 second' WHERE id = $1`, out.Delivery.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if o := w.riderOrders(rider)[0]; o.ID != *out.Delivery.LinkOrderID || o.Status != links.OrderCancelled {
		t.Fatalf("after the window: %s %s", o.ID, o.Status)
	}

	// Can't from the rider's business declines the offer, and the owner is
	// told no one else covers Lekki.
	ref = w.order("Lekki", "none")
	out, err = w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.d.RiderStep(ctx, rider, out.Delivery.LinkOrderID.String(), dispatch.Cant); err != nil {
		t.Fatal(err)
	}
	if o := w.riderOrders(rider)[0]; o.Status != links.OrderDeclined {
		t.Fatalf("after Can't: %s", o.Status)
	}
	if w.alerts(dispatch.AlertNoRider) < 1 {
		t.Error("the owner was not told")
	}

	// Released by the owner after accepting: cancelled on the rider's side.
	ref = w.order("Lekki", "none")
	out, err = w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{Rider: &musa.ID, By: "user:ada"})
	if err != nil {
		t.Fatal(err)
	}
	w.must(*out.Delivery, dispatch.Accept)
	if _, err := w.d.Release(ctx, w.env.Tenant, ref, "user:ada"); err != nil {
		t.Fatal(err)
	}
	if o := w.riderOrders(rider)[0]; o.Status != links.OrderCancelled {
		t.Fatalf("after release: %s", o.Status)
	}
	if got := w.cards(rider, link); got[len(got)-1] != "delivery:released:false" {
		t.Fatalf("the rider's last card: %v", got)
	}

	// A rider with no business on Taskiem is reached as before: no order.
	tunde := w.rider("Tunde", "+2348031110012", "Yaba")
	ref = w.order("Yaba", "none")
	out, err = w.d.Offer(ctx, w.env.Tenant, ref, dispatch.OfferIn{})
	if err != nil || out.Delivery.RiderID != tunde.ID || out.Delivery.LinkOrderID != nil {
		t.Fatalf("a legacy rider: %+v %v", out, err)
	}
}
