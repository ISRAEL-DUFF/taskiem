package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// A customer who asks to pay an invoice online when the business has no
// payments connection that can do it: the page is told (an 'unavailable'
// account) and the business's people are told in their bell, once.
func TestPayUnavailableTellsTheBusiness(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.Invoices.PayUnavailable = w.srv.InvoicePayUnavailable // no Rails: no payments connection at all
	owner := w.tenant(t, "Dangote Stores", "d@pay.test")
	tenant := uuid.MustParse(tenantOf(t, owner))

	x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada Obi", "phone": "+2348031234567"},
		"lines": []map[string]any{{"description": "Rice", "quantity": 1, "unit_kobo": 500000}}, "due_on": "2099-01-31"}, "Idempotency-Key", "p-1")
	sent, err := w.srv.Invoices.Send(ctx, tenant, x["number"].(string), "user:test", "s-1")
	if err != nil {
		t.Fatal(err)
	}
	tok := invoicing.TokenHash(strings.TrimPrefix(sent.Link, "https://forms.taskiem.test/i/"))
	ask := func(kind string) {
		t.Helper()
		var rid *uuid.UUID
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT taskiem_public_invoice_request($1, $2, 0)`, tok, kind).Scan(&rid); err != nil || rid == nil {
			t.Fatalf("request %v %v", err, rid)
		}
		if err := w.srv.Invoices.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		w.srv.WaitBackground()
	}
	ask("transfer")
	ask("transfer")
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT p.status FROM taskiem_public_invoice_payment($1) p WHERE p.kind = 'transfer'`, tok).Scan(&status); err != nil || status != "unavailable" {
		t.Fatalf("the page sees %q %v", status, err)
	}
	var told int
	for _, n := range owner.must(200, "GET", "/v1/notifications", nil)["notifications"].([]any) {
		m := n.(map[string]any)
		if m["title"] == "A customer could not pay online" && strings.Contains(m["body"].(string), "invoice "+x["number"].(string)) && strings.Contains(m["body"].(string), "bank transfer") && strings.Contains(m["body"].(string), "Connect Paystack") {
			told++
		}
	}
	if told != 1 {
		t.Errorf("told %d times, want once: %v", told, owner.must(200, "GET", "/v1/notifications", nil))
	}
}
