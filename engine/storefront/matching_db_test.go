package storefront_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Storefront orders go through the same matching as invoices (spec
// invoices-and-money-log 8.1 to 8.3; docs/matching.md).

type matchWorld struct {
	*world
	log *moneylog.Log
	m   *matching.Matcher
}

func newMatchWorld(t *testing.T) *matchWorld {
	w := newWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.orders.MoneyLog = log
	inv := &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.test", MoneyLog: log, Signal: w.env.Store.DeliverSignalOnce}
	return &matchWorld{world: w, log: log, m: &matching.Matcher{Invoices: inv, Orders: w.orders, PII: w.env.Vault}}
}

// heard records a rail's event in the money log and lets matching look.
func (w *matchWorld) heard(event string, fields map[string]any) {
	w.t.Helper()
	if event == "virtual_account.credited" && fields["credit_id"] == nil {
		fields["credit_id"] = "C-" + fields["reference"].(string) // each its own credit
	}
	if _, err := w.log.RecordEvent(context.Background(), w.env.Tenant, "prod", "paystack", "main", event, fields); err != nil {
		w.t.Fatal(err)
	}
	if err := w.m.Tick(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

func (w *matchWorld) count(q string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.env.DB.Admin.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// once checks an order was paid once: one entry, one match naming the
// order, one paid transition and one run.
func (w *matchWorld) once(ref string) {
	w.t.Helper()
	o := w.order(ref)
	if o.Status != "received" || o.PaidAt == nil || o.RunID == nil {
		w.t.Fatalf("paid: %+v", o)
	}
	if n := w.count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND reference = $2`, w.env.Tenant, storefront.PaymentKey(o.ID)); n != 1 {
		w.t.Errorf("%d money log entries", n)
	}
	if n := w.count(`SELECT count(*) FROM storefront_order_payments WHERE order_id = $1 AND how = 'exact'`, o.ID); n != 1 {
		w.t.Errorf("%d matches", n)
	}
	if n := w.count(`SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'storefront.order_paid' AND target = $2`, w.env.Tenant, ref); n != 1 {
		w.t.Errorf("%d paid transitions", n)
	}
	if n := w.count(`SELECT count(*) FROM runs WHERE tenant_id = $1 AND started_by = $2`, w.env.Tenant, "storefront:"+ref); n != 1 {
		w.t.Errorf("%d runs", n)
	}
	var state string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT s.state FROM money_match_state s JOIN money_entries e ON e.id = s.entry_id WHERE e.reference = $1`,
		storefront.PaymentKey(o.ID)).Scan(&state); err != nil || state != "matched" {
		w.t.Errorf("entry state %q %v", state, err)
	}
}

// noOnlinePayment submits an order taken without online payment (paid on
// delivery, or by transfer) and lets its run start.
func (w *matchWorld) noOnlinePayment(total int64) string {
	w.t.Helper()
	ctx := context.Background()
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "none"})
		return err
	})
	ref := w.submit("order", "none", total)
	if err := w.orders.Drain(ctx); err != nil {
		w.t.Fatal(err)
	}
	w.env.Drain(w.t)
	return ref
}

func (w *matchWorld) atOrder(total int64) (string, storefront.Order) {
	w.t.Helper()
	ctx := context.Background()
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "at_order"})
		return err
	})
	ref := w.submit("order", "at_order", total)
	if err := w.orders.Drain(ctx); err != nil {
		w.t.Fatal(err)
	}
	o := w.order(ref)
	if o.Status != "awaiting_payment" || o.PaymentURL == "" {
		w.t.Fatalf("waiting: %+v", o)
	}
	return ref, o
}

// An order paid through its checkout page is matched exactly and marked
// paid once, whether the provider's event or the order's own payment
// check comes first.
func TestCheckoutPaymentMatchedOnce(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)

	// The event first, then the check.
	ref, o := w.atOrder(250000)
	key := storefront.PaymentKey(o.ID)
	w.heard("payment.received", map[string]any{"payment_id": key, "amount_kobo": int64(250000)})
	w.pay.pay(key)
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	w.heard("payment.received", map[string]any{"payment_id": key, "amount_kobo": int64(250000)}) // delivered again
	w.once(ref)
	var target string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT 'order:' || o.reference FROM storefront_order_payments p JOIN storefront_orders o ON o.id = p.order_id WHERE o.id = $1`, o.ID).Scan(&target); err != nil || target != "order:"+ref {
		t.Errorf("target %q %v", target, err)
	}

	// The check first, then the event.
	ref, o = w.atOrder(180000)
	key = storefront.PaymentKey(o.ID)
	w.pay.pay(key)
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	w.heard("payment.received", map[string]any{"payment_id": key, "amount_kobo": int64(180000)})
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	w.once(ref)

	// Neither is suggested anywhere, nor waits for a person.
	if n := w.count(`SELECT count(*) FROM money_suggestions WHERE tenant_id = $1`, w.env.Tenant); n != 0 {
		t.Errorf("%d suggestions", n)
	}
	// It cannot be undone: the order's run went on with the payment.
	var pay uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM storefront_order_payments WHERE order_id = $1`, o.ID).Scan(&pay); err != nil {
		t.Fatal(err)
	}
	if _, err := w.m.UndoOrderPayment(ctx, w.env.Tenant, pay, "user:ada"); !errors.Is(err, invoicing.ErrState) || !strings.Contains(err.Error(), "cancel or refund") {
		t.Errorf("undo of a fulfilled order: %v", err)
	}
	if o := w.order(ref); o.PaidAt == nil {
		t.Error("a refused undo reopened the order")
	}
}

// A transfer naming the order is suggested for it with its reasons;
// confirmed, the order is paid; undone, it is unpaid again and the money
// goes back to be matched. Another business's orders are never suggested.
func TestOrderSuggestedConfirmedUndone(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)

	// Another business with an order of the same amount and reference
	// words: never a candidate here.
	other := w.env.AddTenant(t)
	otherRef := "AK-OTHER1"
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO storefronts (id, tenant_id, slug, draft, created_by) VALUES ($1, $2, 'other-shop', '{}', 'user:x')`, uuid.New(), other); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO storefront_orders (id, tenant_id, storefront_id, kind, reference, status_hash, status, customer, subject_id, total_kobo, payment)
		SELECT $1, $2, s.id, 'order', $3, '\x00', 'received', '{}', 'x', 300000, 'none' FROM storefronts s WHERE s.tenant_id = $2`, uuid.New(), other, otherRef); err != nil {
		t.Fatal(err)
	}

	ref := w.noOnlinePayment(300000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	before := w.order(ref)
	if before.Status != "received" || before.RunID == nil {
		t.Fatalf("received: %+v", before)
	}
	w.heard("virtual_account.credited", map[string]any{"reference": "TRF FOR " + ref + " " + otherRef, "amount_kobo": int64(300000), "payer_name": "BOLA"})

	var open []matching.Waiting
	w.in(func(tx pgx.Tx) error {
		var err error
		open, err = matching.Open(ctx, tx, w.env.Tenant, 50)
		return err
	})
	if len(open) != 1 || open[0].TargetKind != "order" || open[0].Target != "order:"+ref || open[0].OrderID == nil || open[0].InvoiceID != nil || open[0].DueKobo != 300000 {
		t.Fatalf("suggestions %+v", open)
	}
	why := strings.Join(open[0].Reasons, "; ")
	for _, want := range []string{"same amount as what is due on " + ref, "the reference mentions " + ref, "the payer's name matches the customer's", "the day " + ref + " was placed"} {
		if !strings.Contains(why, want) {
			t.Errorf("reasons %q lack %q", why, want)
		}
	}
	if strings.Contains(why, "Bola") || strings.Contains(why, "BOLA") {
		t.Errorf("a reason names someone: %q", why)
	}
	if w.order(ref).PaidAt != nil {
		t.Fatal("paid without asking")
	}

	res, err := w.m.Confirm(ctx, w.env.Tenant, open[0].ID, "user:ada", false)
	if err != nil || res.Order == nil || res.Target() != "order:"+ref || res.Order.PaidAt == nil || res.Order.Status != "received" {
		t.Fatalf("confirmed %v %+v", err, res)
	}
	var pays []storefront.OrderPayment
	w.in(func(tx pgx.Tx) error {
		pays, err = storefront.PaymentsOf(ctx, tx, w.env.Tenant, res.Order.ID)
		return err
	})
	if len(pays) != 1 || pays[0].How != "suggested" || !pays[0].Undoable {
		t.Fatalf("payments %+v", pays)
	}
	// The same money cannot count towards an invoice too.
	if _, err := w.m.Match(ctx, w.env.Tenant, open[0].EntryID, "INV-0001", "user:ada"); err == nil {
		t.Error("one payment counted twice")
	}
	// Its run was not started again (it had one) or woken.
	if n := w.count(`SELECT count(*) FROM runs WHERE tenant_id = $1 AND started_by = $2`, w.env.Tenant, "storefront:"+ref); n != 1 {
		t.Errorf("%d runs", n)
	}

	// Undone: unpaid again, the money waits for a person, and the wrong
	// suggestion is not offered again.
	o, err := w.m.UndoOrderPayment(ctx, w.env.Tenant, pays[0].ID, "user:ada")
	if err != nil || o.PaidAt != nil || o.Status != "received" {
		t.Fatalf("undone %v %+v", err, o)
	}
	var waiting []matching.Payment
	w.in(func(tx pgx.Tx) error {
		if open, err = matching.Open(ctx, tx, w.env.Tenant, 50); err != nil {
			return err
		}
		waiting, err = matching.Unmatched(ctx, tx, w.env.Tenant, 50)
		return err
	})
	if len(open) != 0 || len(waiting) != 1 {
		t.Fatalf("after undo: %+v %+v", open, waiting)
	}
	// Undoing twice changes nothing.
	if _, err := w.m.UndoOrderPayment(ctx, w.env.Tenant, pays[0].ID, "user:ada"); err != nil {
		t.Errorf("undo again: %v", err)
	}

	// Matched by hand; another business's order cannot be named.
	if _, err := w.m.Match(ctx, w.env.Tenant, waiting[0].EntryID, matching.OrderTarget(otherRef), "user:ada"); !errors.Is(err, invoicing.ErrNotFound) {
		t.Errorf("another business's order: %v", err)
	}
	res, err = w.m.Match(ctx, w.env.Tenant, waiting[0].EntryID, matching.OrderTarget(ref), "user:ada")
	if err != nil || res.Order == nil || res.Order.PaidAt == nil {
		t.Fatalf("by hand %v %+v", err, res)
	}
	var orders []matching.OpenOrder
	w.in(func(tx pgx.Tx) error {
		orders, err = w.m.OpenOrders(ctx, tx, w.env.Tenant, 50)
		return err
	})
	if err != nil || len(orders) != 0 {
		t.Errorf("open orders after paying: %+v %v", orders, err)
	}
	// The order was decided since: the match can no longer be undone.
	w.decide(ref, "decide", "approved")
	w.in(func(tx pgx.Tx) error {
		pays, err = storefront.PaymentsOf(ctx, tx, w.env.Tenant, res.Order.ID)
		return err
	})
	last := pays[len(pays)-1]
	if _, err := w.m.UndoOrderPayment(ctx, w.env.Tenant, last.ID, "user:ada"); !errors.Is(err, invoicing.ErrState) {
		t.Errorf("undo after acceptance: %v", err)
	}
}

// With automatic matching on, a strong and clear suggestion for an order
// is matched alone, and the order paid; off, it waits for a person.
func TestOrderAutoMatch(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)
	w.in(func(tx pgx.Tx) error {
		st, err := invoicing.GetSettings(ctx, tx, w.env.Tenant)
		if err != nil {
			return err
		}
		st.AutoMatch = true
		_, err = invoicing.SaveSettings(ctx, tx, w.env.Tenant, st)
		return err
	})
	// An order paid after acceptance has no payment link yet: not a
	// candidate (its workflow sends the link and waits for that payment).
	later := w.submit("order", "after_accept", 420000)
	ref := w.noOnlinePayment(420000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	w.env.Drain(t)
	w.heard("virtual_account.credited", map[string]any{"reference": "VA-9", "amount_kobo": int64(420000), "payer_name": "BOLA"})
	o := w.order(ref)
	if o.PaidAt == nil {
		t.Fatalf("not matched: %+v", o)
	}
	if l := w.order(later); l.PaidAt != nil || l.Status != "received" {
		t.Errorf("an order without its payment link was paid: %+v", l)
	}
	if n := w.count(`SELECT count(*) FROM storefront_order_payments WHERE order_id = $1 AND how = 'suggested_auto' AND by = 'system:matching' AND reasons <> '[]'`, o.ID); n != 1 {
		t.Errorf("%d automatic matches", n)
	}
	// The kept table gets order:<reference> (copied with the invoices').
	if n := w.count(`SELECT count(*) FROM storefront_order_payments WHERE order_id = $1 AND mirrored_at IS NULL`, o.ID); n != 1 {
		t.Errorf("waiting to be copied: %d", n)
	}
	var tenants []uuid.UUID
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id FROM taskiem_money_unmirrored_payment_tenants()`)
		if err != nil {
			return err
		}
		tenants, err = pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		return err
	}); err != nil || len(tenants) != 1 || tenants[0] != w.env.Tenant {
		t.Errorf("tenants to copy: %v %v", tenants, err)
	}
}
