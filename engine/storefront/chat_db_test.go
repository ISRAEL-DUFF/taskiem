package storefront_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Orders confirmed from customers' messages (chat-five-more-flows.md,
// phase 2): an order of the kind chat, decided when placed; its stock goes
// at once, or when it is paid by transfer; it is ready for a rider as an
// order from the page is; it lapses unpaid after ChatHold, and its
// customer is told each step in their thread (ChatNotify), never by SMS.
func TestChatOrders(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	st := &fakeStock{n: map[string]int64{"1": 10, "2": 10}, keys: map[string]bool{}}
	w.orders.Stock = st
	var mu sync.Mutex
	var told []string
	w.orders.ChatNotify = func(_ context.Context, _ uuid.UUID, o storefront.Order, text string) error {
		mu.Lock()
		defer mu.Unlock()
		if o.Kind != "chat" {
			t.Errorf("told about a %s order", o.Kind)
		}
		told = append(told, text)
		return nil
	}
	place := func(pay, fulfilment string) storefront.Order {
		t.Helper()
		var o storefront.Order
		w.in(func(tx pgx.Tx) error {
			var err error
			o, err = w.orders.PlaceChatTx(ctx, tx, w.env.Tenant, storefront.ChatOrder{
				Customer:   storefront.Customer{Name: "Bola", Phone: "+2348031234567", Address: "12 Herbert Macaulay, Yaba", Message: "no pepper"},
				Lines:      []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 2, PriceKobo: 450000}, {ID: "2", Name: "Chapman", Quantity: 1, PriceKobo: 150000}},
				Fulfilment: fulfilment, DeliveryKobo: 150000, Pay: pay, By: "user:ada",
			})
			return err
		})
		return o
	}

	// Paid at the door: accepted at once, its stock goes now, and it is
	// ready for a rider.
	door := place(storefront.ChatPayOnDelivery, "delivery")
	if door.Kind != "chat" || door.Status != "accepted" || door.Payment != "on_delivery" || door.TotalKobo != 1200000 || door.DecidedAt == nil {
		t.Fatalf("door: %+v", door)
	}
	if door.Customer.Address != "12 Herbert Macaulay, Yaba" || door.Customer.Message != "no pepper" || door.Zone != "" {
		t.Fatalf("customer: %+v zone %q", door.Customer, door.Zone)
	}
	if why := dispatch.ReadyReason(door); why != "" {
		t.Fatalf("not ready for a rider: %s", why)
	}
	if short, err := w.orders.LowerStock(ctx, w.env.Tenant, door.Reference); err != nil || len(short) != 0 {
		t.Fatalf("lower: %v %v", short, err)
	}
	if st.n["1"] != 8 || st.n["2"] != 9 {
		t.Fatalf("stock: %v", st.n)
	}
	// Lowered once, however often it is asked.
	if _, err := w.orders.LowerStock(ctx, w.env.Tenant, door.Reference); err != nil || st.n["1"] != 8 {
		t.Fatalf("lowered twice: %v %v", st.n, err)
	}

	// Paid by transfer: waits for its payment (on the storefront's order
	// action, for its page); a payment marks it paid, its stock goes then,
	// and the customer is told in their thread.
	tr := place(storefront.ChatPayTransfer, "pickup")
	if tr.Status != "awaiting_payment" || tr.Payment != "at_order" || tr.ExpiresAt == nil || tr.DeliveryKobo != 0 || tr.TotalKobo != 1050000 {
		t.Fatalf("transfer: %+v", tr)
	}
	if why := dispatch.ReadyReason(tr); why == "" {
		t.Fatal("an unpaid order ready for a rider")
	}
	// Its page offers a transfer only: it has no card checkout.
	var card bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT p.card FROM storefront_orders o, taskiem_public_order_payment(o.reference, o.status_hash) p WHERE o.reference = $1`,
		tr.Reference).Scan(&card); err != nil || card {
		t.Fatalf("card on a chat order's page: %v %v", card, err)
	}
	var counted storefront.Counted
	w.in(func(tx pgx.Tx) error {
		var err error
		counted, err = w.orders.CountPayment(ctx, tx, w.env.Tenant, tr.Reference, storefront.PaymentIn{AmountKobo: tr.TotalKobo, How: "manual", By: "test"})
		return err
	})
	if err := w.orders.Paid(ctx, w.env.Tenant, counted); err != nil {
		t.Fatal(err)
	}
	if o := w.order(tr.Reference); o.Status != "paid" || o.PaidAt == nil {
		t.Fatalf("paid: %+v", o)
	}
	if st.n["1"] != 6 || st.n["2"] != 8 {
		t.Fatalf("stock after payment: %v", st.n)
	}
	if len(told) != 1 || !strings.Contains(told[0], "Paid, thank you: "+tr.Reference) {
		t.Fatalf("told: %q", told)
	}

	// Not paid in time: it lapses, the customer is told, its stock never
	// went.
	late := place(storefront.ChatPayTransfer, "pickup")
	w.orders.Now = func() time.Time { return time.Now().Add(storefront.ChatHold + time.Hour) }
	defer func() { w.orders.Now = nil }()
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o := w.order(late.Reference); o.Status != "expired" {
		t.Fatalf("lapsed: %+v", o)
	}
	if len(told) != 2 || !strings.Contains(told[1], late.Reference+" was not paid in time") {
		t.Fatalf("told: %q", told)
	}
	if st.n["1"] != 6 {
		t.Fatalf("a lapsed order's stock went: %v", st.n)
	}

	// Its notices go in the thread, not by SMS.
	out, err := w.orders.NotifyCustomer(ctx, w.env.Tenant, door.Reference, "Your order is on its way.", "", "k")
	if err != nil || out["queued"].([]string)[0] != "whatsapp" {
		t.Fatalf("notify: %v %v", out, err)
	}

	// No line, no price or no quantity is refused; a delivery fee too
	// high; pay at the door for a pickup.
	w.in(func(tx pgx.Tx) error {
		for _, bad := range []storefront.ChatOrder{
			{Customer: storefront.Customer{Phone: "+2348031234567"}, Fulfilment: "pickup", Pay: storefront.ChatPayLater},
			{Customer: storefront.Customer{Phone: "+2348031234567"}, Lines: []storefront.Line{{ID: "1", Name: "x", Quantity: 1}}, Fulfilment: "pickup", Pay: storefront.ChatPayLater},
			{Customer: storefront.Customer{Phone: "+2348031234567"}, Lines: []storefront.Line{{ID: "1", Name: "x", Quantity: 1, PriceKobo: 1}}, Fulfilment: "delivery", DeliveryKobo: storefront.MaxZoneFeeKobo + 1, Pay: storefront.ChatPayLater},
			{Customer: storefront.Customer{Phone: "+2348031234567"}, Lines: []storefront.Line{{ID: "1", Name: "x", Quantity: 1, PriceKobo: 1}}, Fulfilment: "pickup", Pay: storefront.ChatPayOnDelivery},
		} {
			if _, err := w.orders.PlaceChatTx(ctx, tx, w.env.Tenant, bad); !errors.Is(err, storefront.ErrInvalid) {
				t.Errorf("%+v: %v", bad, err)
			}
		}
		return nil
	})
}
