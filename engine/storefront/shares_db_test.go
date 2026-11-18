package storefront_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

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

	// Bola's share completes it: the order is paid (asked again once its
	// back-off is over).
	w.pay.pay(refOf(b.URL))
	w.backOffOver(o.ID)
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

// at moves the orders' clock on by d (expiry).
func (w *matchWorld) at(d time.Duration) {
	w.orders.Now = func() time.Time { return time.Now().Add(d) }
}

// backOffOver makes the shares' links of the order due a check again, as
// if their back-off were over.
func (w *matchWorld) backOffOver(order uuid.UUID) {
	w.t.Helper()
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE storefront_order_share_links SET checked_at = now() - interval '1 hour' WHERE order_id = $1 AND checked_at IS NOT NULL`, order); err != nil {
		w.t.Fatal(err)
	}
}

// A share's link not paid yet is asked about again only after its
// back-off (30 seconds, doubling), oldest asked first; its order closed
// over an hour ago, never.
func TestShareChecksBackOff(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)
	ref, o := w.atOrder(300000)
	a, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, uuid.New(), 100000)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := w.orders.CheckPayments(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := w.count(`SELECT checks FROM storefront_order_share_links WHERE order_id = $1`, o.ID); n != 1 {
		t.Fatalf("asked %d times at once, want 1 (backing off)", n)
	}
	w.backOffOver(o.ID)
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT checks FROM storefront_order_share_links WHERE order_id = $1`, o.ID); n != 2 {
		t.Fatalf("asked %d times a minute on, want 2", n)
	}
	// Its order closed over an hour ago: no longer asked (the provider's
	// event still counts it).
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'cancelled', updated_at = now() - interval '2 hours' WHERE id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	w.backOffOver(o.ID)
	w.pay.pay(strings.TrimPrefix(a.URL, "https://checkout.paystack.test/"))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT checks FROM storefront_order_share_links WHERE order_id = $1 AND paid_at IS NULL AND refund_due_at IS NULL`, o.ID); n != 2 {
		t.Fatalf("a closed order's share was asked about: %d", n)
	}
}

// An order part paid by shares that lapses is never silently expired: it
// is refund_due, the business told what to send back; a share paid after
// it lapsed is never marked paid but refund due, the business told too.
func TestSharesLapsedPartPaid(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)
	ref, o := w.atOrder(300000)
	refOf := func(url string) string { return strings.TrimPrefix(url, "https://checkout.paystack.test/") }
	a, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, uuid.New(), 100000)
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, uuid.New(), 200000)
	if err != nil {
		t.Fatal(err)
	}
	w.pay.pay(refOf(a.URL))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	w.at(storefront.PaymentTTL + time.Minute)
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o = w.order(ref); o.Status != "refund_due" || o.PaidKobo != 100000 || !strings.Contains(o.Note, "Paid in parts") {
		t.Fatalf("lapsed part paid: %+v", o)
	}
	if n := w.count(`SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'storefront.order_refund_due' AND target = $2`, w.env.Tenant, ref); n != 1 {
		t.Fatalf("the business was not told: %d", n)
	}
	// Bola pays her share after it lapsed: refund due, not paid.
	w.pay.pay(refOf(b.URL))
	w.backOffOver(o.ID)
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if n := w.count(`SELECT count(*) FROM storefront_order_share_links WHERE order_id = $1 AND refund_due_at IS NOT NULL AND paid_at IS NULL`, o.ID); n != 1 {
		t.Fatalf("the late share is not refund due: %d", n)
	}
	if o = w.order(ref); o.PaidKobo != 100000 || !strings.Contains(o.Note, "A share of ₦2,000") {
		t.Fatalf("after the late share: %+v", o)
	}
	if n := w.count(`SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'storefront.share_refund_due' AND target = $2`, w.env.Tenant, ref); n != 1 {
		t.Fatalf("the late share is not in the chain: %d", n)
	}
}

// Declined or cancelled with only parts paid: refund_due, not declined or
// cancelled silently.
func TestSharesCancelledPartPaid(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)
	ref, _ := w.atOrder(300000)
	a, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, uuid.New(), 100000)
	if err != nil {
		t.Fatal(err)
	}
	w.pay.pay(strings.TrimPrefix(a.URL, "https://checkout.paystack.test/"))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := w.orders.Decide(ctx, w.env.Tenant, ref, "cancel", "", "user:owner")
	if err != nil || out["status"] != "refund_due" {
		t.Fatalf("cancelled part paid: %v %v", out, err)
	}
	if o := w.order(ref); o.Status != "refund_due" || !strings.Contains(o.Note, "Paid in parts") {
		t.Fatalf("the business's order: %+v", o)
	}
	// Refunded by hand: no card page refunds parts.
	if _, err := w.orders.Refund(ctx, w.env.Tenant, ref, "user:owner"); !errors.Is(err, storefront.ErrRefundByHand) {
		t.Fatalf("refund of parts: %v", err)
	}
}

// An order placed together paid in shares (Submission.InShares): no page
// of its own for the whole (its status page offers none), a day to pay,
// and never more than is left: the order's own money over it is refused.
func TestOrderPaidInShares(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "at_order"})
		return err
	})
	ref := storefront.NewReference("ada-kitchen")
	key, hash := storefront.NewStatusKey()
	w.keys[ref] = key
	w.send(ref, hash, storefront.Submission{Kind: "order", Customer: storefront.Customer{Name: "Ada", Phone: "+2348031234567"},
		Basket: []storefront.Line{{ID: "1", Name: "Jollof rice", Quantity: 2, PriceKobo: 150000, LineKobo: 300000}}, TotalKobo: 300000,
		Fulfilment: "pickup", Payment: "at_order", Channel: "app", Submitted: time.Now(), StatusKey: key, InShares: true})
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	if o.Status != "awaiting_payment" || !o.PaidInShares || o.PaymentURL != "" || o.ExpiresAt == nil || time.Until(*o.ExpiresAt) < 23*time.Hour {
		t.Fatalf("paid in shares: %+v", o)
	}
	var payment string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payment FROM taskiem_public_order_status($1, $2)`, ref, hash).Scan(&payment); err != nil || payment != "shares" {
		t.Fatalf("the status page: %q %v", payment, err)
	}
	// A share of all but ₦1,000 paid; then ₦3,000 more matched to it is
	// more than is left: refused, the order not overpaid.
	a, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, uuid.New(), 200000)
	if err != nil {
		t.Fatal(err)
	}
	w.pay.pay(strings.TrimPrefix(a.URL, "https://checkout.paystack.test/"))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	var counted error
	w.in(func(tx pgx.Tx) error {
		_, counted = w.orders.CountPayment(ctx, tx, w.env.Tenant, ref, storefront.PaymentIn{AmountKobo: 300000, How: "manual", By: "user:owner"})
		return nil
	})
	if !errors.Is(counted, storefront.ErrDecision) {
		t.Fatalf("more than is left was counted: %v", counted)
	}
	if o = w.order(ref); o.PaidKobo != 200000 {
		t.Fatalf("overpaid: %+v", o)
	}
}

// The provider's event for a share's own page is matched to it exactly:
// counted once towards its order, whichever of the event and the payment
// check is first.
func TestSharePaidByEvent(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)
	ref, o := w.atOrder(300000)
	a, err := w.orders.ShareLink(ctx, w.env.Tenant, ref, uuid.New(), 100000)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimPrefix(a.URL, "https://checkout.paystack.test/")
	w.heard("payment.received", map[string]any{"payment_id": key, "amount_kobo": int64(100000)})
	if o = w.order(ref); o.PaidKobo != 100000 {
		t.Fatalf("the event did not count the share: %+v", o)
	}
	if n := w.count(`SELECT count(*) FROM storefront_order_share_links WHERE order_id = $1 AND paid_at IS NOT NULL`, o.ID); n != 1 {
		t.Fatalf("the share is not marked paid: %d", n)
	}
	// The check hears it too: still once.
	w.pay.pay(key)
	w.backOffOver(o.ID)
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	w.heard("payment.received", map[string]any{"payment_id": key, "amount_kobo": int64(100000)})
	if o = w.order(ref); o.PaidKobo != 100000 {
		t.Fatalf("counted twice: %+v", o)
	}
	var state string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT s.state FROM money_match_state s JOIN money_entries e ON e.id = s.entry_id WHERE e.reference = $1`, key).Scan(&state); err != nil || state != "matched" {
		t.Fatalf("entry state %q %v", state, err)
	}
}
