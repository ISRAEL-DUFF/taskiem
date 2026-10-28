package storefront_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/storefront"
)

type issued struct {
	order       uuid.UUID
	connection  string
	amount      int64
	description string
	payer       map[string]any
	minutes     int
	account     uuid.UUID
}

// transfers makes the world's orders issue account numbers as invoicing
// would: a row in the invoices' accounts, open, for the order.
func (w *world) transfers() *[]issued {
	var got []issued
	w.orders.IssueTransfer = func(ctx context.Context, tenant, order uuid.UUID, connection string, amount int64, description string, payer map[string]any, minutes int) error {
		id := uuid.New()
		got = append(got, issued{order, connection, amount, description, payer, minutes, id})
		_, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, order_id, seq, kind, connector, connection, payment_id, amount_kobo, status,
			account_number, bank_name, expires_at) VALUES ($1, $2, $3, $4, 'transfer', 'iswallet', 'taskiem', $5, $6, 'open', '9900000001', 'Wema Bank', now() + make_interval(mins => $7))`,
			id, tenant, order, len(got), "tot_"+id.String()[:8], amount, minutes)
		return err
	}
	return &got
}

// A form on another rail than Paystack takes payment by bank transfer
// only: no card page; the account number lengthens the order's hold and
// is issued on the form's connection; a payment into it pays the order
// once and starts its run; and refunding it is by hand.
func TestOrderPaidByTransfer(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	got := w.transfers()
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "at_order",
			Options: storefront.ActionOptions{Connection: "iswallet/taskiem"}})
		return err
	})
	ref := w.submit("order", "at_order", 200000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	o := w.order(ref)
	if o.Status != "awaiting_payment" || o.PaymentURL != "" || o.ExpiresAt == nil {
		t.Fatalf("waiting: %+v", o)
	}
	if _, ok := w.pay.created[storefront.PaymentKey(o.ID)]; ok {
		t.Fatal("a card page was made on another rail's form")
	}
	if err := w.orders.TransferRequested(ctx, w.env.Tenant, o.ID); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("issued %d", len(*got))
	}
	is := (*got)[0]
	if is.connection != "iswallet/taskiem" || is.amount != 200000 || is.description != "Order "+ref || is.payer["email"] != "bola@example.com" || is.minutes < 59 {
		t.Errorf("issued %+v", is)
	}
	if after := w.order(ref); after.ExpiresAt == nil || after.ExpiresAt.Before(time.Now().Add(55*time.Minute)) {
		t.Errorf("the hold was not lengthened: %v", after.ExpiresAt)
	}
	// Paid into it: received, its run started, paid by transfer; again,
	// nothing more.
	for range 2 {
		if err := w.orders.TransferReceived(ctx, w.env.Tenant, o.ID, "tot_x", "iswallet/taskiem", 200000, is.account); err != nil {
			t.Fatal(err)
		}
	}
	o = w.order(ref)
	if o.Status != "received" || o.RunID == nil || o.PaidAt == nil || o.PaidBy != "transfer" {
		t.Fatalf("paid: %+v", o)
	}
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM customer_invoice_accounts WHERE id = $1`, is.account).Scan(&status); err != nil || status != "paid" {
		t.Errorf("the account: %s %v", status, err)
	}
	// No second number for an order no longer waiting.
	if err := w.orders.TransferRequested(ctx, w.env.Tenant, o.ID); !errors.Is(err, storefront.ErrDecision) {
		t.Errorf("a paid order's request: %v", err)
	}
	// Declined: a refund due; approving the refund asks the owner to send
	// it back from their bank, and marking it refunded tells the customer.
	w.env.Drain(t)
	w.decide(ref, "decide", "rejected")
	w.decide(ref, "ask_refund", "approved")
	o = w.order(ref)
	if o.Status != "refund_due" || !strings.Contains(o.Note, "send ₦2,000 back to the customer from your bank") || w.pay.refunded[storefront.PaymentKey(o.ID)] != 0 {
		t.Fatalf("refund by hand: %+v", o)
	}
	if _, err := w.orders.Refund(ctx, w.env.Tenant, ref, "user:ada"); !errors.Is(err, storefront.ErrRefundByHand) {
		t.Errorf("refund: %v", err)
	}
	for range 2 {
		if o, err := w.orders.MarkRefunded(ctx, w.env.Tenant, ref, "user:ada", "sent from GTBank"); err != nil || o.Status != "refunded" {
			t.Fatalf("marked: %+v %v", o, err)
		}
	}
	if _, err := w.orders.MarkRefunded(ctx, w.env.Tenant, w.submitted(t, "order"), "user:ada", ""); !errors.Is(err, storefront.ErrDecision) {
		t.Errorf("an order with nothing to refund: %v", err)
	}
	var texts int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM platform_messages WHERE dedup_key LIKE '%refund:'||$1||'%'`, ref).Scan(&texts); err != nil || texts == 0 {
		t.Errorf("the customer was not told: %d %v", texts, err)
	}
}

// submitted is a fresh order taken from the inbox, by its reference.
func (w *world) submitted(t *testing.T, kind string) string {
	ref := w.submit(kind, "after_accept", 100000)
	if err := w.orders.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ref
}

// Paid after the business accepts, on another rail: the payment link is
// the order's status page, where it is paid by transfer.
func TestPaymentLinkOnAnotherRail(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.transfers()
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "after_accept",
			Options: storefront.ActionOptions{Connection: "iswallet/taskiem"}})
		return err
	})
	ref := w.submitted(t, "order")
	w.env.Drain(t)
	w.decide(ref, "decide", "approved")
	o := w.order(ref)
	if o.Status != "awaiting_payment" || o.PaymentURL != "" || o.ExpiresAt == nil || o.ExpiresAt.Before(time.Now().Add(23*time.Hour)) {
		t.Fatalf("after accepting: %+v", o)
	}
	link, err := w.orders.PaymentLink(ctx, w.env.Tenant, ref)
	if err != nil || !strings.HasPrefix(link["url"].(string), "https://ada-kitchen.shops.test/o/"+ref+"?k=") {
		t.Fatalf("link %v %v", link, err)
	}
	if _, ok := w.pay.created[storefront.PaymentKey(o.ID)]; ok {
		t.Error("a card page was made")
	}
}

// A form's connection is a name, or connector/name.
func TestFormConnection(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	for c, ok := range map[string]bool{"main": true, "iswallet/taskiem": true, "paystack/shop-2": true, "a/b/c": false, "has space": false, "/main": false} {
		err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "at_order", Options: storefront.ActionOptions{Connection: c}})
			return err
		})
		if (err == nil) != ok {
			t.Errorf("%q: %v", c, err)
		}
	}
}
