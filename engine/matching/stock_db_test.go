package matching_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/stock"
)

// shelf is a catalogue's stock that takes every change (engine/stock
// tests the stock side).
type shelf struct{}

func (shelf) Read(context.Context, uuid.UUID, string) (int64, bool, error) { return 100, true, nil }
func (shelf) Change(context.Context, uuid.UUID, string, int64, int64, string) (bool, error) {
	return true, nil
}

// A counter sale's money is the sale's: its cash takings are never
// offered for an invoice nor matched to one by hand; a transfer the
// matcher offered for an invoice, once a sale takes it, is offered no
// more, and the old offer cannot be confirmed.
func TestSalesMoneyIsNotAnInvoices(t *testing.T) {
	w := newWorld(t)
	st := &stock.Stock{Pool: w.env.DB.App, Table: shelf{}}
	x := w.invoice("Ada Obi", 4500000)
	line := []stock.SaleLine{{ItemID: "rice", Item: "Rice", Quantity: 1, PriceKobo: 4500000}}

	cash, err := st.RecordSale(ctx, w.env.Tenant, stock.SaleIn{PaidHow: stock.Cash, Lines: line, Actor: "user:ada", Key: "s-cash"}, w.log)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if open := w.open(); len(open) != 0 {
		t.Fatalf("the takings offered: %+v", open)
	}
	if _, err := w.m.Match(ctx, w.env.Tenant, *cash.EntryID, x.Number, "user:ada"); err == nil {
		t.Fatal("the takings paid an invoice")
	}
	var towards string
	w.in(func(tx pgx.Tx) error {
		if err := moneylog.LockEntry(ctx, tx, *cash.EntryID); err != nil {
			return err
		}
		towards, err = moneylog.CountedTowards(ctx, tx, w.env.Tenant, *cash.EntryID)
		return err
	})
	if towards != "sale "+cash.Reference {
		t.Fatalf("counted towards %q", towards)
	}

	// A transfer sale waits; the transfer comes and is offered for the
	// invoice first; then the sale takes it.
	sale, err := st.RecordSale(ctx, w.env.Tenant, stock.SaleIn{PaidHow: stock.Transfer, Lines: line, Actor: "user:ada", Key: "s-transfer", SoldAt: time.Now()}, w.log)
	if err != nil || sale.Status != "waiting" {
		t.Fatalf("sale: %+v %v", sale, err)
	}
	w.heard("payment.received", map[string]any{"payment_id": "PAY-9", "amount_kobo": int64(4500000)})
	open := w.open()
	if len(open) != 1 || open[0].Number != x.Number {
		t.Fatalf("offered: %+v", open)
	}
	if got, err := st.MatchSales(ctx, w.env.Tenant); err != nil || got != 1 {
		t.Fatalf("matched %d, %v", got, err)
	}
	if left := w.open(); len(left) != 0 {
		t.Fatalf("still offered: %+v", left)
	}
	if _, err := w.m.Confirm(ctx, w.env.Tenant, open[0].ID, "user:ada", false); err == nil {
		t.Fatal("the old offer was confirmed")
	}
	if y := w.read(x.Number); y.Status != "sent" || len(y.Payments) != 0 {
		t.Fatalf("invoice: %+v", y)
	}
}
