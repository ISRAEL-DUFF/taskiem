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

// Suggested matches over the API: listed with their reasons, rejected or
// confirmed by a manager, matched by hand, and undone like any payment.
func TestMatchingAPI(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := addMember(t, w, owner, "vic", "viewer")
	tenant := tenantIDNamed(t, w, "Ada Foods")
	m := &matching.Matcher{Invoices: w.srv.Invoices, PII: w.env.Vault}

	invoice := func(name string, kobo int) string {
		x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": name, "phone": "+2348031234567"}, "amount_kobo": kobo})
		owner.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "send-"+name)
		return x["number"].(string)
	}
	first, second := invoice("Bola Ade", 900000), invoice("Chidi Eze", 450000)
	heard := func(ref string, kobo int64, payer string) {
		if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "virtual_account.credited",
			map[string]any{"reference": ref, "amount_kobo": kobo, "payer_name": payer}); err != nil {
			t.Fatal(err)
		}
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	heard("VA-1", 900000, "BOLA ADE")
	heard("VA-2", 450000, "")

	list := viewer.must(200, "GET", "/v1/money/suggestions", nil)["suggestions"].([]any)
	if len(list) != 2 {
		t.Fatalf("suggestions %v", list)
	}
	byNumber := map[string]map[string]any{}
	for _, s := range list {
		byNumber[s.(map[string]any)["number"].(string)] = s.(map[string]any)
	}
	a, b := byNumber[first], byNumber[second]
	if a == nil || b == nil || len(a["reasons"].([]any)) < 2 {
		t.Fatalf("suggestions %v", list)
	}
	for _, s := range list {
		for _, r := range s.(map[string]any)["reasons"].([]any) {
			if r == "BOLA ADE" || bytes.Contains([]byte(r.(string)), []byte("BOLA")) {
				t.Errorf("a reason names the payer: %q", r)
			}
		}
	}
	viewer.must(403, "POST", "/v1/money/suggestions/"+a["id"].(string)+"/confirm", nil)

	paid := owner.must(200, "POST", "/v1/money/suggestions/"+a["id"].(string)+"/confirm", nil)
	if paid["status"] != "paid" {
		t.Fatalf("confirmed %v", paid)
	}
	owner.must(409, "POST", "/v1/money/suggestions/"+a["id"].(string)+"/confirm", nil)
	owner.must(200, "POST", "/v1/money/suggestions/"+b["id"].(string)+"/reject", nil)
	if l := owner.must(200, "GET", "/v1/money/suggestions", nil)["suggestions"].([]any); len(l) != 0 {
		t.Fatalf("left %v", l)
	}
	// Turned down, it waits to be matched by hand.
	waiting := viewer.must(200, "GET", "/v1/money/unmatched", nil)["payments"].([]any)
	if len(waiting) != 1 || waiting[0].(map[string]any)["entry_id"] != b["entry_id"] {
		t.Fatalf("unmatched %v", waiting)
	}
	// By hand, then undone: the invoice moves back and keeps both rows.
	entry := b["entry_id"].(string)
	owner.must(400, "POST", "/v1/money/entries/"+entry+"/match", map[string]any{})
	x := owner.must(200, "POST", "/v1/money/entries/"+entry+"/match", map[string]any{"invoice": second})
	if x["status"] != "paid" {
		t.Fatalf("matched %v", x)
	}
	pays := x["payments"].([]any)
	undone := owner.must(200, "POST", "/v1/invoices/"+second+"/payments/"+pays[0].(map[string]any)["id"].(string)+"/undo", nil)
	if undone["status"] != "sent" || len(undone["payments"].([]any)) != 2 {
		t.Errorf("undone %v", undone)
	}
	if l := owner.must(200, "GET", "/v1/money/unmatched", nil)["payments"].([]any); len(l) != 1 {
		t.Errorf("undone payment not waiting: %v", l)
	}
	// The setting.
	st := owner.must(200, "GET", "/v1/invoice-settings", nil)
	st["auto_match"] = true
	if got := owner.must(200, "PUT", "/v1/invoice-settings", st); got["auto_match"] != true {
		t.Errorf("settings %v", got)
	}
}
