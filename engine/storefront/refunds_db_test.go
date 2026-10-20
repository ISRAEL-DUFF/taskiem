package storefront_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/storefront"
)

// A refund needs the owner's word and is made once; a refused one leaves
// the order refund due, with the reason.
func TestRefund(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	ref := w.submit("order", "after_accept", 300000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	if _, err := w.orders.Refund(ctx, w.env.Tenant, ref, "user:ada"); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("nothing paid: %v", err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'refund_due', payment_ref = 'pay-1', paid_at = now() WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}

	w.pay.refuse = "Insufficient balance to process this refund"
	if _, err := w.orders.Refund(ctx, w.env.Tenant, ref, "user:ada"); !errors.Is(err, storefront.ErrRefund) {
		t.Fatalf("refused: %v", err)
	}
	if o := w.order(ref); o.Status != "refund_due" || !strings.Contains(o.Note, "Insufficient balance") {
		t.Fatalf("after a refusal: %+v", o)
	}

	w.pay.refuse = ""
	before := w.messages(ref)
	o, err := w.orders.Refund(ctx, w.env.Tenant, ref, "user:ada")
	if err != nil || o.Status != "refunded" || w.pay.refunded["pay-1"] != 1 {
		t.Fatalf("refunded: %+v %v %v", o, err, w.pay.refunded)
	}
	if o := w.order(ref); o.Status != "refunded" || o.Note != "" {
		t.Fatalf("recorded: %+v", o)
	}
	if n := w.messages(ref); n != before+1 {
		t.Fatalf("the customer was not told: %d messages", n)
	}
	if _, err := w.orders.Refund(ctx, w.env.Tenant, ref, "user:ada"); err != nil || w.pay.refunded["pay-1"] != 1 {
		t.Fatalf("twice: %v %v", err, w.pay.refunded)
	}
}
