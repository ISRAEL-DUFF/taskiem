package storefront_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/google/uuid"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Part payments (docs/spec/fronts.md §5.4, F5): money less than an order
// asks is a part of it, counted towards what it costs, and the order moves
// on once its parts reach its deposit or total; a later step asks for the
// balance on an order paid before, with its own checkout page, and the
// payment that completes it tells the run what it asked and what is left.
func TestPartPayments(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)
	entry := func(ref string, kobo int64) *uuid.UUID {
		t.Helper()
		id, _, err := w.log.Record(ctx, w.env.Tenant, moneylog.Entry{Direction: "in", Kind: "account_credit", Status: "settled", Channel: "paystack/main",
			Reference: ref, Event: "virtual_account.credited", AmountKobo: &kobo, Source: "event"})
		if err != nil {
			t.Fatal(err)
		}
		return &id
	}
	count := func(ref string, e *uuid.UUID, kobo int64) (storefront.Counted, error) {
		var c storefront.Counted
		err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			var err error
			c, err = w.orders.CountPayment(ctx, tx, w.env.Tenant, ref, storefront.PaymentIn{EntryID: e, AmountKobo: kobo, How: "manual", By: "user:ada"})
			return err
		})
		return c, err
	}

	// The first payment in two parts.
	ref := w.noOnlinePayment(300000)
	c, err := count(ref, entry("PART-1", 100000), 100000)
	if err != nil || c.Became {
		t.Fatalf("first part: %+v %v", c, err)
	}
	o := w.order(ref)
	if o.PaidKobo != 100000 || o.PaidAt != nil || !o.Matchable() || o.AskedKobo() != 200000 || o.PaymentState() != "part_paid" || o.BalanceKobo() != 200000 {
		t.Fatalf("after a part: %+v", o)
	}
	if c, err = count(ref, entry("PART-2", 200000), 200000); err != nil || !c.Became {
		t.Fatalf("second part: %+v %v", c, err)
	}
	if o = w.order(ref); o.PaidKobo != 300000 || o.PaidAt == nil || o.Matchable() || o.PaymentState() != "paid" || o.AskedKobo() != 0 {
		t.Fatalf("paid: %+v", o)
	}
	if _, err := count(ref, entry("PART-3", 5000), 5000); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("more on a paid order: %v", err)
	}
	if _, err := w.orders.PaymentLink(ctx, w.env.Tenant, ref, 0); !errors.Is(err, storefront.ErrDecision) {
		t.Fatalf("nothing left to pay: %v", err)
	}

	// A late fee: a later step asks for the balance with its own page.
	ref2, o2 := w.atOrder(250000)
	w.pay.pay(storefront.PaymentKey(o2.ID))
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o2 = w.order(ref2); o2.PaidKobo != 250000 || o2.PaidAt == nil {
		t.Fatalf("paid at order: %+v", o2)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET extra_kobo = 50000 WHERE reference = $1`, ref2); err != nil {
		t.Fatal(err)
	}
	link, err := w.orders.PaymentLink(ctx, w.env.Tenant, ref2, 0)
	if err != nil || link["amount_kobo"] != int64(50000) || link["balance_kobo"] != int64(50000) || link["total"] != "₦500" {
		t.Fatalf("balance link: %v %v", link, err)
	}
	again, err := w.orders.PaymentLink(ctx, w.env.Tenant, ref2, 20000)
	if err != nil || again["url"] != link["url"] || again["amount_kobo"] != int64(50000) {
		t.Fatalf("the open step again: %v %v", again, err)
	}
	o2 = w.order(ref2)
	step := storefront.PaymentKey(o2.ID) + "_1"
	if o2.PaySeq != 1 || o2.PayToKobo == nil || *o2.PayToKobo != 300000 || o2.AskedKobo() != 50000 || !o2.Matchable() || o2.Status != "received" || o2.PaymentURL != "https://checkout.paystack.test/"+step {
		t.Fatalf("step open: %+v", o2)
	}
	if w.pay.created[step].AmountKobo != 50000 {
		t.Fatalf("the step's checkout: %+v", w.pay.created[step])
	}
	w.pay.pay(step)
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.orders.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o2 = w.order(ref2); o2.PayToKobo != nil || o2.PaidKobo != 300000 || o2.BalanceKobo() != 0 || o2.Status != "received" || o2.PaymentState() != "paid" {
		t.Fatalf("step paid: %+v", o2)
	}
	if n := w.count(`SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'storefront.order_step_paid' AND target = $2`, w.env.Tenant, ref2); n != 1 {
		t.Fatalf("%d step payments", n)
	}
	var payload string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload::text FROM signals WHERE tenant_id = $1 AND event = 'storefront.payment' AND correlation = $2 ORDER BY received_at DESC LIMIT 1`, w.env.Tenant, ref2).Scan(&payload); err != nil {
		t.Fatalf("the step's signal: %v", err)
	}
	for _, want := range []string{`"amount_kobo": 50000`, `"balance_kobo": 0`, `"status": "paid"`} {
		if !contains(payload, want) {
			t.Errorf("signal %s: no %s", payload, want)
		}
	}

	// A step paid in parts by matched money; the refund is by hand, since
	// a card refund gives back one checkout payment.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET extra_kobo = 90000 WHERE reference = $1`, ref2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.orders.PaymentLink(ctx, w.env.Tenant, ref2, 30000); err != nil {
		t.Fatal(err)
	}
	if c, err = count(ref2, entry("STEP-1", 10000), 10000); err != nil || c.Step {
		t.Fatalf("part of a step: %+v %v", c, err)
	}
	if c, err = count(ref2, entry("STEP-2", 20000), 20000); err != nil || !c.Step {
		t.Fatalf("rest of a step: %+v %v", c, err)
	}
	if o2 = w.order(ref2); o2.PaidKobo != 330000 || o2.BalanceKobo() != 10000 || o2.PayToKobo != nil || o2.PaySeq != 2 || o2.RefundKobo() != 330000 {
		t.Fatalf("after two steps: %+v", o2)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && (s[:len(sub)] == sub || contains(s[1:], sub))
}
