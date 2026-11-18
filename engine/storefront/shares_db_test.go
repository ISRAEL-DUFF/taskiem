package storefront_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// Shares of an order placed together (people-chatting §11 C, C2): each
// share has its own pay link, made once whoever asks for it; a paid share
// is a part of the order, counted once; the order is paid when its shares
// reach what it costs; a share more than is left, or of an order that
// cannot be paid now, is refused.
func TestSharePayments(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)
	ref, o := w.atOrder(300000)
	ada, bola := uuid.New(), uuid.New()
	refOf := func(url string) string { return strings.TrimPrefix(url, "https://checkout.paystack.test/") }

	a, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, ada, 100000)
	if err != nil || a.URL == "" || a.AmountKobo != 100000 || a.PaidAt != nil {
		t.Fatalf("ada's link: %+v %v", a, err)
	}
	// Asked again (by the placer for her): the same page, whatever amount.
	if again, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, ada, 5000); err != nil || again.URL != a.URL || again.AmountKobo != 100000 {
		t.Fatalf("again: %+v %v", again, err)
	}
	if _, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, bola, 300001); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("more than is left: %v", err)
	}
	b, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, bola, 200000)
	if err != nil || b.URL == a.URL {
		t.Fatalf("bola's link: %+v %v", b, err)
	}

	// Ada pays: a part, counted once however often the check runs.
	w.pay.pay(refOf(a.URL))
	for range 2 {
		if err := w.orders.CheckPayments(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if o = w.order(ref); o.PaidKobo != 100000 || o.PaidAt != nil || o.PaymentState() != "part_paid" || o.Status != "awaiting_payment" {
		t.Fatalf("after ada's share: %+v", o)
	}
	if got, _ := w.orders.ShareLink(ctx, w.env.Tenant, ref, ada, 100000); got.PaidAt == nil {
		t.Fatalf("ada's share not marked paid: %+v", got)
	}
	if n := w.count(`SELECT count(*) FROM storefront_order_payments WHERE order_id = $1`, o.ID); n != 1 {
		t.Fatalf("parts counted: %d", n)
	}

	// Bola's share completes it: the order is paid.
	w.pay.pay(refOf(b.URL))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o = w.order(ref); o.PaidKobo != 300000 || o.PaidAt == nil || o.PaymentState() != "paid" {
		t.Fatalf("after bola's share: %+v", o)
	}
	// Nothing is left: no new share's link.
	if _, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, uuid.New(), 100); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("a share of a paid order: %v", err)
	}
}
