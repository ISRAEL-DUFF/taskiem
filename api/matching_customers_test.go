package api_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Customers' permanent accounts, split payments and payer memory over the
// API: an account registered, a credit into it paying the oldest invoice
// with the rest as credit, a held credit with its reason, a suggestion
// confirmed in parts, and a remembered payer forgotten.
func TestMatchingCustomersAPI(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := addMember(t, w, owner, "vic", "viewer")
	tenant := tenantIDNamed(t, w, "Ada Foods")
	m := &matching.Matcher{Invoices: w.srv.Invoices, PII: w.env.Vault}
	tick := func() {
		t.Helper()
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	invoice := func(customer map[string]any, kobo int, due string) map[string]any {
		body := map[string]any{"customer": customer, "amount_kobo": kobo}
		if due != "" {
			body["due_on"] = due
		}
		x := owner.must(201, "POST", "/v1/invoices", body)
		return owner.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "send-"+x["number"].(string))["invoice"].(map[string]any)
	}
	ebube := map[string]any{"name": "Ebube Eze", "phone": "+2348031230001", "key": "student:1"}
	newer := invoice(ebube, 300000, "2099-12-01")
	older := invoice(ebube, 200000, "2099-09-01")

	reg := map[string]any{"customer": ebube, "channel": "paystack/main", "account_number": "9912345678", "reference": "tnt:student:1", "bank_name": "Wema Bank"}
	viewer.must(403, "POST", "/v1/customer-accounts", reg)
	owner.must(400, "POST", "/v1/customer-accounts", map[string]any{"customer": ebube, "channel": "nope", "account_number": "1", "reference": "x"})
	acct := owner.must(200, "POST", "/v1/customer-accounts", reg)
	if acct["account_number"] != "9912345678" || acct["customer"].(map[string]any)["name"] != "E•••" {
		t.Fatalf("account %v", acct)
	}
	other := map[string]any{"customer": map[string]any{"name": "Chioma", "phone": "+2348031230002"}, "channel": "paystack/main", "account_number": "9912345678", "reference": "tnt:student:1"}
	owner.must(409, "POST", "/v1/customer-accounts", other)

	// ₦6,000 into the account: the older invoice, the newer, ₦1,000 credit.
	amount := int64(600000)
	if _, _, err := log.Record(ctx, tenant, moneylog.Entry{Direction: "in", Kind: "account_credit", Status: "settled", Channel: "paystack/main",
		Reference: "tnt:student:1", AccountNumber: "9912345678", Event: "virtual_account.credited", Source: "event", AmountKobo: &amount}); err != nil {
		t.Fatal(err)
	}
	// And one naming the account with another account number: held (its own
	// event name keeps it a separate movement in the log).
	held := int64(50000)
	if _, _, err := log.Record(ctx, tenant, moneylog.Entry{Direction: "in", Kind: "account_credit", Status: "settled", Channel: "paystack/main",
		Reference: "tnt:student:1", AccountNumber: "1111111111", Event: "virtual_account.credited.x", Source: "event", AmountKobo: &held}); err != nil {
		t.Fatal(err)
	}
	tick()
	for _, n := range []string{older["number"].(string), newer["number"].(string)} {
		if x := viewer.must(200, "GET", "/v1/invoices/"+n, nil)["invoice"].(map[string]any); x["status"] != "paid" {
			t.Errorf("%s %v", n, x["status"])
		}
	}
	cust := viewer.must(200, "GET", "/v1/invoices/"+older["number"].(string)+"/customer", nil)
	if cust["credit_kobo"].(float64) != 100000 || len(cust["accounts"].([]any)) != 1 || cust["revealed"] != false {
		t.Fatalf("customer %v", cust)
	}
	viewer.must(403, "GET", "/v1/invoices/"+older["number"].(string)+"/customer?reveal=true", nil)
	waiting := viewer.must(200, "GET", "/v1/money/unmatched", nil)["payments"].([]any)
	if len(waiting) != 1 || waiting[0].(map[string]any)["held_reason"] != matching.HeldOtherAccount {
		t.Fatalf("held %v", waiting)
	}

	// A suggestion for more than is due offers a split, and is confirmed in
	// parts.
	ada := map[string]any{"name": "Ada Obi", "phone": "+2348031230003"}
	a1, a2 := invoice(ada, 200000, "2099-09-01"), invoice(ada, 300000, "2099-10-01")
	if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "virtual_account.credited",
		map[string]any{"reference": "VA-ADA", "amount_kobo": int64(600000), "payer_name": "AUNTY NGOZI ADA OBI"}); err != nil {
		t.Fatal(err)
	}
	tick()
	list := owner.must(200, "GET", "/v1/money/suggestions", nil)["suggestions"].([]any)
	if len(list) == 0 {
		t.Fatal("no suggestion")
	}
	s := list[0].(map[string]any)
	split, _ := s["split"].(map[string]any)
	if split == nil || len(split["parts"].([]any)) != 2 || split["credit_kobo"].(float64) != 100000 {
		t.Fatalf("split %v", s)
	}
	owner.must(400, "POST", "/v1/money/suggestions/"+s["id"].(string)+"/confirm", map[string]any{"parts": []any{
		map[string]any{"invoice": a1["number"], "amount_kobo": 500000}, map[string]any{"invoice": a2["number"], "amount_kobo": 300000}}})
	got := owner.must(200, "POST", "/v1/money/suggestions/"+s["id"].(string)+"/confirm", map[string]any{"parts": []any{
		map[string]any{"invoice": a1["number"], "amount_kobo": 200000}, map[string]any{"invoice": a2["number"], "amount_kobo": 300000}}, "credit_rest": true})
	if parts := got["parts"].([]any); len(parts) != 2 || got["credit_kobo"].(float64) != 100000 || got["left_kobo"].(float64) != 0 ||
		parts[0].(map[string]any)["amount_kobo"].(float64) != 200000 {
		t.Fatalf("confirmed %v", got)
	}
	// The payer is remembered for the customer, masked, and can be forgotten.
	cust = owner.must(200, "GET", "/v1/invoices/"+a1["number"].(string)+"/customer", nil)
	payers := cust["payers"].([]any)
	if len(payers) != 1 || payers[0].(map[string]any)["payer"] != "A•••" || cust["credit_kobo"].(float64) != 100000 {
		t.Fatalf("payers %v", cust)
	}
	id := payers[0].(map[string]any)["id"].(string)
	viewer.must(403, "DELETE", "/v1/money/payers/"+id, nil)
	owner.must(200, "DELETE", "/v1/money/payers/"+id, nil)
	owner.must(404, "DELETE", "/v1/money/payers/"+id, nil)
	if p := owner.must(200, "GET", "/v1/invoices/"+a1["number"].(string)+"/customer", nil)["payers"].([]any); len(p) != 0 {
		t.Errorf("forgotten payer still listed: %v", p)
	}
	// The next invoice sent to the customer takes the credit.
	sent := invoice(ada, 150000, "")
	if sent["paid_kobo"].(float64) != 100000 || sent["status"] != "part_paid" {
		t.Errorf("credit not counted: %v", sent)
	}
}
