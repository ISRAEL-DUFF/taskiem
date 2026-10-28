package storefront_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/storefront"
)

// orderRails is a Paystack-like rail opening one-time accounts.
type orderRails struct {
	mu   sync.Mutex
	paid map[string]int64
	ins  []map[string]any
}

func (f *orderRails) Resolve(_ context.Context, _ uuid.UUID, connection string) (invoicing.Rail, error) {
	if connection != "main" && connection != "paystack/main" {
		return invoicing.Rail{}, invoicing.ErrNoRail
	}
	return invoicing.Rail{Connector: "paystack", Connection: "main", Name: "Paystack", Link: true,
		Transfer: &connector.TransferFacts{OnLate: "returned", OnWrongAmount: "returned", MinMinutes: 15, MaxMinutes: 480}}, nil
}

func (f *orderRails) Key(_ invoicing.Rail, capability string, k effects.KeyInput) (string, error) {
	return "tsk_" + capability + "_" + k.Seed + "_" + k.StepID, nil
}

func (f *orderRails) Call(_ context.Context, _ uuid.UUID, r invoicing.Rail, capability string, in map[string]any, k effects.KeyInput) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch capability {
	case "collect_transfer":
		f.ins = append(f.ins, in)
		pid, _ := f.Key(r, capability, k)
		return map[string]any{"payment_id": pid, "account_number": "9900112233", "bank_name": "Wema Bank", "account_name": "Shop / Ada",
			"expires_at": time.Now().Add(time.Duration(in["expires_minutes"].(int)) * time.Minute).UTC().Format(time.RFC3339)}, nil
	case "get_payment":
		id, _ := in["payment_id"].(string)
		if a, ok := f.paid[id]; ok {
			return map[string]any{"payment_id": id, "status": "paid", "amount_kobo": a}, nil
		}
		return map[string]any{"payment_id": id, "status": "pending"}, nil
	}
	return nil, errors.New("no such capability")
}

// transferWorld wires the storefront to the invoices' accounts, as
// cmd/taskiem does.
func transferWorld(t *testing.T) (*matchWorld, *invoicing.Invoices, *orderRails) {
	w := newMatchWorld(t)
	rails := &orderRails{paid: map[string]int64{}}
	inv := w.m.Invoices
	inv.Rails = rails
	w.orders.IssueTransfer = func(ctx context.Context, tenant, order uuid.UUID, connection string, amount int64, description string, payer map[string]any, minutes int) error {
		_, err := inv.IssueForOrder(ctx, tenant, order, invoicing.OrderTransfer{Connection: connection, AmountKobo: amount, Description: description, Payer: payer, Minutes: minutes})
		return err
	}
	inv.OrderRequested = w.orders.TransferRequested
	inv.OrderReceived = func(ctx context.Context, tenant, order uuid.UUID, r invoicing.Received) error {
		return w.orders.TransferReceived(ctx, tenant, order, r.PaymentID, r.Connection, r.AmountKobo, r.Account)
	}
	ctx := context.Background()
	w.in(func(tx pgx.Tx) error {
		_, err := storefront.SaveAction(ctx, tx, w.env.Tenant, storefront.Action{Kind: "order", Enabled: true, Payment: "at_order"})
		return err
	})
	return w, inv, rails
}

// ask asks for an account number from the order's status page, as the
// public role, and lets the api role's queue issue it.
func (w *matchWorld) ask(inv *invoicing.Invoices, ref string) (status, number string, card bool) {
	w.t.Helper()
	ctx := context.Background()
	pub := w.env.DB.PublicPool(w.t, 1)
	hash := storefront.StatusHash(w.statusKey(ref))
	var id *string
	if err := pub.QueryRow(ctx, `SELECT taskiem_public_order_request($1, $2)::text`, ref, hash).Scan(&id); err != nil || id == nil {
		w.t.Fatalf("request: %v %v", id, err)
	}
	if err := inv.Drain(ctx); err != nil {
		w.t.Fatal(err)
	}
	var held *time.Time
	if err := pub.QueryRow(ctx, `SELECT COALESCE(status, ''), COALESCE(account_number, ''), card, held_until FROM taskiem_public_order_payment($1, $2)`, ref, hash).
		Scan(&status, &number, &card, &held); err != nil {
		w.t.Fatal(err)
	}
	if held == nil || held.Before(time.Now().Add(55*time.Minute)) {
		w.t.Errorf("held until %v", held)
	}
	return status, number, card
}

// An order on a Paystack form may be paid by transfer too: the status
// page asks, the invoices' queue issues on the form's rail, and the
// rail's payment event pays the order exactly, once.
func TestOrderTransferPaidByTheRailsEvent(t *testing.T) {
	w, inv, rails := transferWorld(t)
	ctx := context.Background()
	ref := w.submit("order", "at_order", 200000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if o := w.order(ref); o.PaymentURL == "" {
		t.Fatalf("no card page on a Paystack form: %+v", o)
	}
	status, number, card := w.ask(inv, ref)
	if status != "open" || number != "9900112233" || !card || len(rails.ins) != 1 || rails.ins[0]["amount_kobo"] != int64(200000) || rails.ins[0]["description"] != "Order "+ref {
		t.Fatalf("issued %s %s %v %v", status, number, card, rails.ins)
	}
	// Asked again: the open account, not another.
	if _, _, _ = w.ask(inv, ref); len(rails.ins) != 1 {
		t.Errorf("a second account: %d", len(rails.ins))
	}
	o := w.order(ref)
	pid := "tsk_collect_transfer_order:" + o.ID.String() + "_transfer:1"
	for range 2 {
		w.heard("payment.received", map[string]any{"payment_id": pid, "amount_kobo": int64(200000)})
	}
	o = w.order(ref)
	if o.Status != "received" || o.RunID == nil || o.PaidBy != "transfer" {
		t.Fatalf("paid: %+v", o)
	}
	if n := w.count(`SELECT count(*) FROM storefront_order_payments WHERE order_id = $1 AND how = 'exact'`, o.ID); n != 1 {
		t.Errorf("%d matches", n)
	}
	if n := w.count(`SELECT count(*) FROM customer_invoice_accounts WHERE order_id = $1 AND status = 'paid'`, o.ID); n != 1 {
		t.Errorf("%d accounts paid", n)
	}
	// The status page asks for no more.
	var id *string
	if err := w.env.DB.PublicPool(t, 1).QueryRow(ctx, `SELECT taskiem_public_order_request($1, $2)::text`, ref, storefront.StatusHash(w.statusKey(ref))).Scan(&id); err != nil || id != nil {
		t.Errorf("a paid order's request: %v %v", id, err)
	}
}

// A rail that can be asked (get_payment): the invoices' checker finds the
// payment and hands it to the order.
func TestOrderTransferFoundByTheChecker(t *testing.T) {
	w, inv, rails := transferWorld(t)
	ctx := context.Background()
	ref := w.submit("order", "at_order", 150000)
	if err := w.orders.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := w.ask(inv, ref); status != "open" {
		t.Fatalf("not issued: %s", status)
	}
	o := w.order(ref)
	rails.paid["tsk_collect_transfer_order:"+o.ID.String()+"_transfer:1"] = 150000
	if err := inv.CheckPayments(ctx); err != nil {
		t.Fatal(err)
	}
	if o = w.order(ref); o.Status != "received" || o.PaidBy != "transfer" || o.RunID == nil {
		t.Fatalf("paid: %+v", o)
	}
	if n := w.count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND reference = $2`, w.env.Tenant, "tsk_collect_transfer_order:"+o.ID.String()+"_transfer:1"); n != 1 {
		t.Errorf("%d money log entries", n)
	}
}
