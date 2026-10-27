package invoicing_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
)

// entry records a rail's payment.received in the money log.
func (w *world) entry(ref string, amount, fee int64) uuid.UUID {
	w.t.Helper()
	e, ok := moneylog.FromEvent("iswallet/main", "payment.received", map[string]any{"payment_id": ref, "amount_kobo": amount, "fee_kobo": fee})
	if !ok {
		w.t.Fatal("no entry")
	}
	var id uuid.UUID
	w.in(func(tx pgx.Tx) error {
		var err error
		id, _, err = (&moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}).RecordTx(context.Background(), tx, w.env.Tenant, e)
		return err
	})
	return id
}

// count counts amount of entry towards invoice number.
func (w *world) count(number string, entry *uuid.UUID, ref string, amount int64) invoicing.Invoice {
	w.t.Helper()
	var x invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		ctx := context.Background()
		if _, _, err := w.inv.RecordPayment(ctx, tx, w.env.Tenant, number, invoicing.PaymentIn{AmountKobo: amount, PaymentID: ref, EntryID: entry,
			Connection: "iswallet/main", How: "exact", By: "system"}); err != nil {
			return err
		}
		var err error
		x, err = w.inv.Get(ctx, tx, w.env.Tenant, number)
		return err
	})
	return x
}

// lastSMS is the newest SMS queued to a customer, opened (the sender's
// name before it).
func (w *world) lastSMS() string {
	w.t.Helper()
	var text string
	w.in(func(tx pgx.Tx) error {
		ctx := context.Background()
		var env map[string]any
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT body FROM platform_messages WHERE dedup_key LIKE '%:sms' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&env); err != nil {
			return err
		}
		v, err := w.env.Vault.OpenTx(ctx, tx, w.env.Tenant, env)
		if err != nil {
			return err
		}
		m, _ := v.(map[string]any)
		text, _ = m["text"].(string)
		return nil
	})
	return text
}

// An invoice paid by one transfer whose fee the rail reported carries the
// fee on its payment, in the paid and payment triggers, and in the
// receipt's {fee_line}; one paid by part of a transfer, or by cash, says
// nothing of a fee.
func TestReceiptSaysTheBanksFee(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	var business string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT name FROM tenants WHERE id = $1`, w.env.Tenant).Scan(&business); err != nil {
		t.Fatal(err)
	}
	send := func(kobo int64) invoicing.Invoice {
		x := w.create(invoicing.Draft{Customer: ada, AmountKobo: kobo})
		if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "send:"+x.Number); err != nil {
			t.Fatal(err)
		}
		return x
	}
	const text = "Thank you for invoice {number}.{fee_line} See it: {link}"

	// One whole transfer: ₦25,000 sent, ₦350 kept.
	x := send(2500000)
	e := w.entry("tot_one", 2500000, 35000)
	x = w.count(x.Number, &e, "tot_one", 2500000)
	if x.Status != "paid" || len(x.Payments) != 1 || x.Payments[0].FeeKobo == nil || *x.Payments[0].FeeKobo != 35000 {
		t.Fatalf("paid %s %+v", x.Status, x.Payments)
	}
	if sent, fee, ok := x.Fee(); !ok || sent != 2500000 || fee != 35000 {
		t.Fatalf("fee %d %d %v", sent, fee, ok)
	}
	body := invoicing.PaidTrigger(x, "https://forms.test/i/t")["body"].(map[string]any)
	if body["fee_kobo"] != int64(35000) || body["fee"] != "₦350" || body["received_kobo"] != int64(2465000) || body["received"] != "₦24,650" {
		t.Errorf("paid trigger %v", body)
	}
	pay := invoicing.PaymentTrigger(x, x.Payments[0], nil, "", "")["body"].(map[string]any)
	pm := pay["payment"].(map[string]any)
	if pm["fee_kobo"] != int64(35000) || pm["received"] != "₦24,650" || pay["fee_kobo"] != nil {
		t.Errorf("payment trigger %v", pay)
	}
	if _, err := w.inv.NotifyCustomer(ctx, w.env.Tenant, x.Number, text, "", "receipt:"+x.Number); err != nil {
		t.Fatal(err)
	}
	want := "Thank you for invoice " + x.Number + ". You sent ₦25,000; ₦24,650 reached " + business + " after the bank's fee of ₦350. See it: https://forms.test/i/"
	if got := w.lastSMS(); !strings.Contains(got, want) {
		t.Errorf("receipt %q, want %q…", got, want)
	}

	// A transfer split over two invoices: the fee is the transfer's, not
	// either invoice's, so neither receipt names one.
	a, b := send(1000000), send(1000000)
	split := w.entry("tot_split", 2000000, 30000)
	a = w.count(a.Number, &split, "tot_split", 1000000)
	w.count(b.Number, &split, "tot_split", 1000000)
	if a.Payments[0].FeeKobo != nil {
		t.Errorf("a split part has a fee: %d", *a.Payments[0].FeeKobo)
	}
	if _, _, ok := a.Fee(); ok {
		t.Error("a split invoice has a fee")
	}
	if _, ok := invoicing.PaidTrigger(a, "")["body"].(map[string]any)["fee_kobo"]; ok {
		t.Error("a split invoice's trigger has a fee")
	}
	if _, err := w.inv.NotifyCustomer(ctx, w.env.Tenant, a.Number, text, "", "receipt:"+a.Number); err != nil {
		t.Fatal(err)
	}
	if got := w.lastSMS(); !strings.Contains(got, "Thank you for invoice "+a.Number+". See it: ") {
		t.Errorf("split receipt %q", got)
	}

	// A transfer and cash: what the cash cost is unknown, so no fee.
	c := send(2000000)
	part := w.entry("tot_part", 1000000, 15000)
	w.count(c.Number, &part, "tot_part", 1000000)
	c = w.count(c.Number, nil, "cash-1", 1000000)
	if c.Status != "paid" || c.Payments[0].FeeKobo == nil {
		t.Fatalf("mixed %s %+v", c.Status, c.Payments)
	}
	if _, _, ok := c.Fee(); ok {
		t.Error("an invoice paid partly in cash has a fee")
	}
}
