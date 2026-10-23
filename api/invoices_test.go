package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Invoices through the API (decision 0045, T3): drafts, sending, the
// customer masked unless revealed, voiding, settings, permissions, and no
// way to mark one paid but a payment.
func TestInvoices(t *testing.T) {
	w := newWorld(t)
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := addMember(t, w, owner, "vic", "viewer")

	draft := map[string]any{"customer": map[string]any{"name": "Bola Ade", "phone": "+2348031234567", "email": "bola@example.com"},
		"lines": []map[string]any{{"description": "Bags of rice", "quantity": 2, "unit_kobo": 4500000}}, "due_on": "2099-01-31"}
	owner.must(400, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Bola"}, "amount_kobo": 1000})
	x := owner.must(201, "POST", "/v1/invoices", draft, "Idempotency-Key", "inv-1")
	if x["number"] != "INV-0001" || x["status"] != "draft" || x["total_kobo"] != float64(9000000) || x["customer"].(map[string]any)["name"] != "B•••" {
		t.Fatalf("created %v", x)
	}
	// The same key answers the same draft.
	if again := owner.must(201, "POST", "/v1/invoices", draft, "Idempotency-Key", "inv-1"); again["id"] != x["id"] {
		t.Fatalf("retry made another: %v", again)
	}
	id := x["id"].(string)
	viewer.must(403, "POST", "/v1/invoices", draft)
	if l := viewer.must(200, "GET", "/v1/invoices", nil)["invoices"].([]any); len(l) != 1 {
		t.Fatalf("viewer list %v", l)
	}
	viewer.must(403, "GET", "/v1/invoices/"+id+"?reveal=true", nil)
	got := owner.must(200, "GET", "/v1/invoices/INV-0001?reveal=true", nil)
	if got["revealed"] != true || got["invoice"].(map[string]any)["customer"].(map[string]any)["phone"] != "+2348031234567" {
		t.Fatalf("revealed %v", got)
	}

	// A draft changes; sending fixes it.
	draft["note"] = "Thank you"
	owner.must(200, "PUT", "/v1/invoices/"+id, draft)
	sent := owner.must(200, "POST", "/v1/invoices/"+id+"/send", nil, "Idempotency-Key", "send-1")
	if !strings.HasPrefix(sent["link"].(string), "https://forms.taskiem.test/i/") || sent["invoice"].(map[string]any)["status"] != "sent" || len(sent["queued"].([]any)) != 2 {
		t.Fatalf("sent %v", sent)
	}
	owner.must(409, "PUT", "/v1/invoices/"+id, draft)
	viewer.must(403, "POST", "/v1/invoices/"+id+"/send", nil)

	// A payment, as T4 and T5 will record them, then undone over the API.
	var pay uuid.UUID
	ctx := context.Background()
	tenant := tenantIDNamed(t, w, "Ada Foods")
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		y, _, err := w.srv.Invoices.RecordPayment(ctx, tx, tenant, "INV-0001", invoicing.PaymentIn{AmountKobo: 4000000, PaymentID: "tsk_abc", How: "exact", By: "test"})
		if err == nil {
			pay = y.Payments[0].ID
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if y := owner.must(200, "GET", "/v1/invoices/"+id, nil)["invoice"].(map[string]any); y["status"] != "part_paid" || y["due_kobo"] != float64(5000000) {
		t.Fatalf("part paid %v", y)
	}
	viewer.must(403, "POST", "/v1/invoices/"+id+"/payments/"+pay.String()+"/undo", nil)
	if y := owner.must(200, "POST", "/v1/invoices/"+id+"/payments/"+pay.String()+"/undo", nil); y["status"] != "sent" || y["paid_kobo"] != float64(0) {
		t.Fatalf("undone %v", y)
	}

	owner.must(400, "POST", "/v1/invoices/"+id+"/void", map[string]any{"reason": ""})
	if v := owner.must(200, "POST", "/v1/invoices/"+id+"/void", map[string]any{"reason": "Wrong price"}); v["status"] != "void" || v["void_reason"] != "Wrong price" {
		t.Fatalf("void %v", v)
	}
	owner.must(409, "POST", "/v1/invoices/"+id+"/send", nil)
	if l := owner.must(200, "GET", "/v1/invoices?status=void", nil)["invoices"].([]any); len(l) != 1 {
		t.Fatalf("void list %v", l)
	}
	owner.must(400, "GET", "/v1/invoices?status=lost", nil)
	owner.must(404, "GET", "/v1/invoices/INV-0099", nil)

	// Settings: the prefix and numbering, never backwards.
	if st := owner.must(200, "GET", "/v1/invoice-settings", nil); st["prefix"] != "INV-" || st["next_number"] != float64(2) {
		t.Fatalf("settings %v", st)
	}
	viewer.must(403, "PUT", "/v1/invoice-settings", map[string]any{"prefix": "AF-", "next_number": 10, "connection": "main", "fee_paid_by": "business", "allow_part_payments": false})
	owner.must(400, "PUT", "/v1/invoice-settings", map[string]any{"prefix": "AF-", "next_number": 1, "connection": "main", "fee_paid_by": "business", "allow_part_payments": false})
	owner.must(200, "PUT", "/v1/invoice-settings", map[string]any{"prefix": "AF-", "next_number": 10, "connection": "main", "fee_paid_by": "customer", "allow_part_payments": true})
	if y := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Chi", "email": "chi@example.com"}, "amount_kobo": 250000}); y["number"] != "AF-0010" || y["fee_paid_by"] != "customer" || y["allow_part_payments"] != true {
		t.Fatalf("with settings %v", y)
	}
	// Another tenant sees none of it.
	other := w.tenant(t, "Other", "o@other.test")
	other.must(404, "GET", "/v1/invoices/"+id, nil)
	if l := other.must(200, "GET", "/v1/invoices", nil)["invoices"].([]any); len(l) != 0 {
		t.Fatalf("leak %v", l)
	}
}

func tenantIDNamed(t *testing.T, w *world, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM tenants WHERE name = $1`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
