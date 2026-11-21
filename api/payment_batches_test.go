package api_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"

	payconn "github.com/israel-duff/taskiem/connectors/payments"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

// Paying several people (paying people, phase 3): a batch's rows are each
// prepared as one payment is, a row failing a check held out with why
// before anything moves; one passkey sends the rest, each its own payment;
// staff's rows over their limit wait, and the owner's one passkey gives the
// OK for all of them; a pasted list is read a line at a time; the failed
// rows are paid again as a new batch; last time's is copied.
func TestPaySeveral(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.LoginBurst = 100
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = payRails{}
	for _, c := range []*connector.Connector{payconn.New(w.srv.PaymentsOps()), paystack.New(paystack.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	banks := []map[string]any{{"name": "Access Bank", "code": "044"}, {"name": "Guaranty Trust Bank", "code": "058"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner := w.tenant(t, "Ada Foods", "ada@batch.test")
	biz := uuid.MustParse(tenantOf(t, owner))
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	for _, s := range []map[string]any{
		{"name": "bayo", "title": "Bayo Rice Mills", "kind": "bank_account", "bank_name": "Access Bank", "account_number": "0123456789"},
		{"name": "chidi", "title": "Chidi Okeke", "kind": "bank_account", "bank_name": "Guaranty Trust Bank", "account_number": "0987654321"},
	} {
		owner.must(201, "POST", "/v1/saved", s)
	}
	key := addPasskey(t, owner, "Phone")
	runOf := func(payment string) uuid.UUID {
		t.Helper()
		var r uuid.UUID
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT run_id FROM payments WHERE id = $1`, payment).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}

	// Prepared: two rows, one held out with why.
	b := owner.must(201, "POST", "/v1/payment-batches", map[string]any{"title": "Riders, week 45", "rows": []map[string]any{
		{"payee": "bayo", "amount_kobo": 1000000, "purpose": "delivery"},
		{"payee": "chidi", "amount_kobo": 2000000, "purpose": "delivery"},
		{"payee": "nobody", "amount_kobo": 500000, "purpose": "delivery"},
	}})["batch"].(map[string]any)
	id := b["id"].(string)
	held := b["held"].([]any)
	if b["count"] != float64(2) || b["total_kobo"] != float64(3000000) || len(held) != 1 || held[0].(map[string]any)["code"] != "no_payee" || held[0].(map[string]any)["row"] != float64(2) {
		t.Fatalf("prepared: %v", b)
	}
	owner.must(400, "POST", "/v1/payment-batches", map[string]any{"rows": []map[string]any{}})

	// One passkey for the batch; each row sent as its own payment.
	if e := owner.must(403, "POST", "/v1/payment-batches/"+id+"/confirm", map[string]any{}); e["step_up"] == nil {
		t.Fatalf("no passkey: %v", e)
	}
	sent := owner.must(202, "POST", "/v1/payment-batches/"+id+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay.batch", "batch:"+id)})["batch"].(map[string]any)
	if sent["status"] != "sent" {
		t.Fatalf("sent: %v", sent)
	}
	for _, x := range sent["payments"].([]any) {
		if p := x.(map[string]any); p["status"] != "sending" || p["run_id"] == nil {
			t.Fatalf("a row: %v", p)
		}
	}
	owner.must(409, "POST", "/v1/payment-batches/"+id+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay.batch", "batch:"+id)})

	// A row fails at the bank: paid again as a new batch, its row alone.
	first := sent["payments"].([]any)[0].(map[string]any)["id"].(string)
	if _, err := w.srv.PaymentsOps().Finish(ctx, biz, runOf(first), uuid.MustParse(first), "failed", "", "", "The bank refused it.", nil); err != nil {
		t.Fatal(err)
	}
	again := owner.must(201, "POST", "/v1/payment-batches/"+id+"/again", nil)["batch"].(map[string]any)
	if again["count"] != float64(1) || again["total_kobo"] != float64(1000000) {
		t.Fatalf("again: %v", again)
	}
	// Last time's, copied.
	if cp := owner.must(201, "POST", "/v1/payment-batches", map[string]any{"copy_of": id})["batch"].(map[string]any); cp["count"] != float64(2) || cp["title"] != "Riders, week 45" {
		t.Fatalf("copied: %v", cp)
	}

	// Staff: a row over their limit waits; the owner's one passkey gives
	// the OK for every row waiting.
	owner.must(201, "PUT", "/v1/roles/payer", map[string]any{"permissions": []string{"money.pay"}})
	clerk := w.member(t, owner, "clerk@batch.test", "payer")
	ckey := addPasskey(t, clerk, "Phone")
	owner.must(200, "PUT", "/v1/pay-limits/payer", map[string]any{"one_kobo": 1500000, "day_kobo": 10000000})
	cb := clerk.must(201, "POST", "/v1/payment-batches", map[string]any{"rows": []map[string]any{
		{"payee": "bayo", "amount_kobo": 1200000, "purpose": "stock"},
		{"payee": "chidi", "amount_kobo": 1800000, "purpose": "stock"},
	}})["batch"].(map[string]any)
	cid := cb["id"].(string)
	cs := clerk.must(202, "POST", "/v1/payment-batches/"+cid+"/confirm", map[string]any{"passkey": stepUp(t, clerk, ckey, "money.pay.batch", "batch:"+cid)})["batch"].(map[string]any)
	if cs["waiting"] != float64(1) {
		t.Fatalf("staff's batch: %v", cs)
	}
	clerk.must(403, "POST", "/v1/payment-batches/"+cid+"/approve", map[string]any{})
	ok := owner.must(200, "POST", "/v1/payment-batches/"+cid+"/approve", map[string]any{"passkey": stepUp(t, owner, key, "money.pay.approve", "batch:"+cid)})["batch"].(map[string]any)
	for _, r := range ok["results"].([]any) {
		if r.(map[string]any)["code"] != nil {
			t.Fatalf("approved: %v", ok)
		}
	}
	// The waiting row's run now passes its check.
	var wid string
	for _, x := range ok["payments"].([]any) {
		if p := x.(map[string]any); p["needs_ok"] != "" {
			wid = p["id"].(string)
		}
	}
	if out, err := w.srv.PaymentsOps().Check(ctx, biz, runOf(wid), uuid.MustParse(wid),
		payconn.CheckIn{AmountKobo: 1800000, BankCode: "058", AccountNumber: "0987654321", NeedsOK: true}); err != nil || out["status"] != "sending" {
		t.Fatalf("after the owner's OK: %v %v", out, err)
	}

	// A pasted list, a line at a time.
	lines := owner.must(200, "POST", "/v1/payment-batches/read", map[string]any{"text": "Chidi 0987654321 GTBank 45k\nnot a payment\nMusa 0123456789 30k"})["lines"].([]any)
	l0, l1, l2 := lines[0].(map[string]any), lines[1].(map[string]any), lines[2].(map[string]any)
	if l0["name"] != "Chidi" || l0["account_number"] != "0987654321" || l0["bank_name"] != "Guaranty Trust Bank" || l0["amount_kobo"] != float64(4500000) || l0["payee"] != "chidi" || l0["problem"] != nil {
		t.Fatalf("line 1: %v", l0)
	}
	if l1["problem"] == nil || l2["problem"] == nil || l2["payee"] != "bayo" {
		t.Fatalf("lines 2 and 3: %v %v", l1, l2)
	}

	// Cancelled: what was not sent is let go.
	c := owner.must(201, "POST", "/v1/payment-batches", map[string]any{"rows": []map[string]any{{"payee": "bayo", "amount_kobo": 100000}}})["batch"].(map[string]any)
	if got := owner.must(200, "POST", "/v1/payment-batches/"+c["id"].(string)+"/cancel", nil)["batch"].(map[string]any); got["status"] != "cancelled" || got["count"] != float64(0) {
		t.Fatalf("cancelled: %v", got)
	}
	if list := owner.must(200, "GET", "/v1/payment-batches", nil)["batches"].([]any); len(list) < 5 {
		t.Fatalf("batches: %d", len(list))
	}
}
