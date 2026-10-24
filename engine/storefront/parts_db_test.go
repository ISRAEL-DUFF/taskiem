package storefront_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// Invoices and orders count their parts of one payment along one path
// (moneylog.Parts.Count, Parts.Undo): a part of the same entry towards an
// invoice and one towards an order, the entry never counted past what it
// counts for, the same part again a no-op, and each undone once.
func TestOnePathCountsInvoiceAndOrderParts(t *testing.T) {
	ctx := context.Background()
	w := newMatchWorld(t)
	inv := &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.test", MoneyLog: w.log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
	ref := w.noOnlinePayment(300000)
	invoice := func(kobo int64) invoicing.Invoice {
		t.Helper()
		var x invoicing.Invoice
		w.in(func(tx pgx.Tx) error {
			var err error
			x, err = inv.Create(ctx, tx, w.env.Tenant, invoicing.Draft{Customer: invoicing.Customer{Name: "Bola", Phone: "+2348031234567"}, AmountKobo: kobo}, "user:ada")
			return err
		})
		if _, err := inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "send:"+x.Number); err != nil {
			t.Fatal(err)
		}
		return x
	}
	x := invoice(200000)
	amount := int64(500000)
	entry, _, err := w.log.Record(ctx, w.env.Tenant, moneylog.Entry{Direction: "in", Kind: "account_credit", Status: "settled", Channel: "paystack/main",
		Reference: "SPLIT-1", Event: "virtual_account.credited", AmountKobo: &amount, Source: "event"})
	if err != nil {
		t.Fatal(err)
	}
	left := func() int64 {
		t.Helper()
		var n int64
		w.in(func(tx pgx.Tx) error {
			var err error
			n, _, err = moneylog.EntryLeft(ctx, tx, entry)
			return err
		})
		return n
	}
	in := invoicing.PaymentIn{AmountKobo: 200000, PaymentID: "SPLIT-1", EntryID: &entry, Connection: "paystack/main", How: "manual", By: "user:ada"}
	toOrder := func(kobo int64) storefront.PaymentIn {
		return storefront.PaymentIn{EntryID: &entry, AmountKobo: kobo, How: "manual", By: "user:ada"}
	}

	var ic invoicing.Counted
	var oc storefront.Counted
	w.in(func(tx pgx.Tx) error {
		var err error
		if ic, err = inv.CountPayment(ctx, tx, w.env.Tenant, x.Number, in); err != nil {
			return err
		}
		oc, err = w.orders.CountPayment(ctx, tx, w.env.Tenant, ref, toOrder(300000))
		return err
	})
	if ic.PaymentID == uuid.Nil || !ic.Became || ic.Invoice.Status != "paid" {
		t.Errorf("invoice part %+v", ic)
	}
	if oc.PaymentID == uuid.Nil || !oc.Became || oc.Order.PaidAt == nil {
		t.Errorf("order part %+v", oc)
	}
	if n := left(); n != 0 {
		t.Errorf("left %d, want 0", n)
	}

	// The same part again is a no-op; any more of the entry is refused,
	// naming what it counts towards, by either owner.
	w.in(func(tx pgx.Tx) error {
		again, err := inv.CountPayment(ctx, tx, w.env.Tenant, x.Number, in)
		if err != nil || again.PaymentID != uuid.Nil || again.Became {
			t.Errorf("again %+v %v", again, err)
		}
		return nil
	})
	y := invoice(100000)
	over := in
	over.AmountKobo = 100
	counted := "already counted towards invoice " + x.Number + " and order " + ref
	err = db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		_, err := inv.CountPayment(ctx, tx, w.env.Tenant, y.Number, over)
		return err
	})
	if !errors.Is(err, invoicing.ErrState) || !strings.Contains(err.Error(), counted) {
		t.Errorf("an invoice over the entry: %v", err)
	}
	ref2 := w.noOnlinePayment(100000)
	err = db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		_, err := w.orders.CountPayment(ctx, tx, w.env.Tenant, ref2, toOrder(100000))
		return err
	})
	if !errors.Is(err, storefront.ErrDecision) || !strings.Contains(err.Error(), counted) {
		t.Errorf("an order over the entry: %v", err)
	}

	// Each part is undone once: a second undo writes nothing.
	for range 2 {
		w.in(func(tx pgx.Tx) error {
			if _, err := inv.UndoPayment(ctx, tx, w.env.Tenant, x.Number, ic.PaymentID, "user:ada"); err != nil {
				return err
			}
			_, got, err := w.orders.UndoPayment(ctx, tx, w.env.Tenant, oc.PaymentID, "user:ada")
			if err == nil && got != entry {
				t.Errorf("undo named entry %s", got)
			}
			return err
		})
	}
	if n := w.count(`SELECT count(*) FROM customer_invoice_payments WHERE undoes = $1`, ic.PaymentID); n != 1 {
		t.Errorf("%d undoings of the invoice part", n)
	}
	if n := w.count(`SELECT count(*) FROM storefront_order_payments WHERE undoes = $1`, oc.PaymentID); n != 1 {
		t.Errorf("%d undoings of the order part", n)
	}
	if n := left(); n != amount {
		t.Errorf("left %d after undoing, want %d", n, amount)
	}
	if o := w.order(ref); o.PaidAt != nil || o.Status != "received" {
		t.Errorf("order after undo %+v", o)
	}
}
