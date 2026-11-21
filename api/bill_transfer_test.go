package api_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Paying a bill by transfer, in the chat (the payments flow, part 1): the
// buyer asks for an account number for a bill; the request crosses to the
// seller's invoice, whose provider issues it for what is due; it comes
// back on the bill, one at a time, a new one once it has expired; paid,
// the bill says so and offers the same seller's next bill. Only those who
// may pay bills ask for or see the number; a bill with nothing due, or
// queried, gets none; a number that could not be had is said in words.
func TestBillByTransfer(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	rails := &chatRails{}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce, Rails: rails}
	w.srv.Invoices.Answered = w.srv.InvoiceAnswered
	seller, buyer, sellerID, buyerID := linkedPair(t, w)
	viewer := addMember(t, w.world, buyer, "vic", "viewer")
	tick := func() {
		t.Helper()
		if _, err := w.srv.LinkMirrorTick(ctx); err != nil {
			t.Fatal(err)
		}
		w.srv.WaitBackground()
	}
	drain := func() {
		t.Helper()
		if err := w.srv.Invoices.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		w.srv.WaitBackground()
	}
	send := func(key string, unit int) map[string]any {
		t.Helper()
		x := seller.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada's Kitchen", "key": "link:" + buyerID},
			"lines": []map[string]any{{"description": "Rice, 50kg", "quantity": 1, "unit_kobo": unit}}}, "Idempotency-Key", key)
		seller.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s-"+key)
		tick()
		return x
	}
	x := send("t-1", 8300000)
	next := send("t-2", 1200000)
	billOf := func(x map[string]any) map[string]any {
		t.Helper()
		return buyer.must(200, "GET", "/v1/bills/"+x["number"].(string), nil)
	}
	b := billOf(x)
	id := b["id"].(string)
	if ways := stringOf(b["ways_to_pay"]); ways != "[transfer checkout]" || b["transfer"] != nil {
		t.Fatalf("ways to pay: %v %v", b["ways_to_pay"], b["transfer"])
	}

	// Asked: the request crosses; asked again meanwhile, nothing more.
	viewer.must(403, "POST", "/v1/bills/"+id+"/transfer", map[string]any{})
	if got := buyer.must(202, "POST", "/v1/bills/"+id+"/transfer", map[string]any{}); got["transfer"].(map[string]any)["state"] != "asking" {
		t.Fatalf("asked: %v", got)
	}
	buyer.must(202, "POST", "/v1/bills/"+id+"/transfer", map[string]any{})
	drain()
	tr := buyer.must(200, "GET", "/v1/bills/"+id+"/transfer", nil)["transfer"].(map[string]any)
	if tr["state"] != "open" || tr["account_number"] != "9900112233" || tr["bank_name"] != "Wema Bank" || tr["amount_kobo"] != float64(8300000) || tr["expires_at"] == nil {
		t.Fatalf("the number: %v", tr)
	}
	if rails.calls != 1 {
		t.Fatalf("issued %d numbers", rails.calls)
	}
	// One at a time: the open one again.
	if got := buyer.must(200, "POST", "/v1/bills/"+id+"/transfer", map[string]any{})["transfer"].(map[string]any); got["account_number"] != "9900112233" || rails.calls != 1 {
		t.Fatalf("again: %v (%d)", got, rails.calls)
	}
	// Those who may not pay bills see no number.
	if got := viewer.must(200, "GET", "/v1/bills/"+id, nil); got["transfer"] != nil {
		t.Errorf("a viewer saw the number: %v", got["transfer"])
	}
	// Expired: a new one.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE bills SET transfer = jsonb_set(transfer, '{expires_at}', to_jsonb(now() - interval '1 minute')) WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE customer_invoice_accounts SET expires_at = now() - interval '1 minute' WHERE invoice_id = $1`, x["id"]); err != nil {
		t.Fatal(err)
	}
	if got := buyer.must(200, "GET", "/v1/bills/"+id+"/transfer", nil)["transfer"].(map[string]any); got["state"] != "expired" {
		t.Fatalf("expired: %v", got)
	}
	buyer.must(202, "POST", "/v1/bills/"+id+"/transfer", map[string]any{"longer": true})
	drain()
	if rails.calls != 2 {
		t.Fatalf("a new number: %d", rails.calls)
	}

	// Paid: the bill says so, and offers the next one.
	var pid string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payment_id FROM customer_invoice_accounts WHERE invoice_id = $1 ORDER BY seq DESC LIMIT 1`, x["id"]).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if err := w.srv.Invoices.Received(ctx, uuid.MustParse(sellerID), uuid.MustParse(x["id"].(string)), invoicing.Received{PaymentID: pid, AmountKobo: 8300000}); err != nil {
		t.Fatal(err)
	}
	tick()
	paid := billOf(x)
	if tr := paid["transfer"].(map[string]any); paid["status"] != "paid" || tr["state"] != "paid" || tr["next"] != billOf(next)["id"] {
		t.Fatalf("paid: %v %v", paid["status"], paid["transfer"])
	}
	if e := buyer.must(409, "POST", "/v1/bills/"+id+"/transfer", map[string]any{}); e["code"] != "nothing_due" {
		t.Errorf("nothing due: %v", e)
	}

	// A queried bill waits for the query to be settled.
	nid := billOf(next)["id"].(string)
	buyer.must(200, "POST", "/v1/bills/"+nid+"/query", map[string]any{"reason": "Short by two bags"})
	if e := buyer.must(409, "POST", "/v1/bills/"+nid+"/transfer", map[string]any{}); e["code"] != "disputed" {
		t.Errorf("queried: %v", e)
	}

	// A number that could not be had is said on the bill.
	third := send("t-3", 500000)
	tid := billOf(third)["id"].(string)
	buyer.must(202, "POST", "/v1/bills/"+tid+"/transfer", map[string]any{})
	w.srv.InvoiceAnswered(ctx, invoicing.Request{Tenant: uuid.MustParse(sellerID), Invoice: uuid.MustParse(third["id"].(string)), Kind: "transfer", Channel: "bill",
		ReplyTo: map[string]any{"buyer": buyerID, "bill": tid, "link": billOf(third)["link"]}}, invoicing.Account{}, invoicing.ErrLimit)
	if got := buyer.must(200, "GET", "/v1/bills/"+tid+"/transfer", nil)["transfer"].(map[string]any); got["state"] != "failed" || got["problem"] == nil {
		t.Fatalf("could not be had: %v", got)
	}
}
