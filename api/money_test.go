package api_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/leaks"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
)

// Statements, cash and expenses, fee rates and leaks over the API.
func TestMoneyAPI(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := addMember(t, w, owner, "vic", "viewer")
	tenant := tenantIDNamed(t, w, "Ada Foods")

	// A statement in a bank's own columns: refused whole until matched.
	csv := "Trans Date,Narration,Debit,Credit,Ref\n20/10/2026,TRF FROM BOLA,,\"25,000.00\",FT1\n21/10/2026,DIESEL,\"12,000.00\",,FT2\n"
	bad := owner.must(422, "POST", "/v1/money/statements", map[string]any{"channel": "gtbank/current", "csv": csv})
	if bad["code"] != "statement_unreadable" || len(bad["missing"].([]any)) == 0 {
		t.Fatalf("unmatched columns %v", bad)
	}
	cols := map[string]any{"date": "Trans Date", "description": "Narration", "in": "Credit", "out": "Debit", "reference": "Ref"}
	viewer.must(403, "POST", "/v1/money/statements", map[string]any{"channel": "gtbank/current", "csv": csv, "columns": cols})
	got := owner.must(200, "POST", "/v1/money/statements", map[string]any{"channel": "gtbank/current", "csv": csv, "columns": cols})
	if got["added"] != float64(2) || got["skipped"] != float64(0) {
		t.Fatalf("upload %v", got)
	}
	// The next upload uses the remembered columns, and skips what is in.
	if again := owner.must(200, "POST", "/v1/money/statements", map[string]any{"channel": "gtbank/current", "csv": csv}); again["skipped"] != float64(2) {
		t.Fatalf("again %v", again)
	}
	if f := viewer.must(200, "GET", "/v1/money/statement-format?channel=gtbank/current", nil); f["columns"].(map[string]any)["in"] != "Credit" {
		t.Errorf("format %v", f)
	}
	// A line Taskiem cannot read: nothing added, and where.
	broken := owner.must(422, "POST", "/v1/money/statements", map[string]any{"channel": "gtbank/current", "csv": csv + "someday,X,,5,FT3\n"})
	if p := broken["problems"].([]any); len(p) != 1 || p[0].(map[string]any)["row"] != float64(4) {
		t.Errorf("broken %v", broken)
	}

	// Cash and expenses, once per key.
	exp := map[string]any{"kind": "expense", "amount_kobo": 1200000, "description": "Diesel", "vendor": "Total", "category": "fuel", "occurred_at": "2026-10-21"}
	first := owner.must(201, "POST", "/v1/money/entries", exp, "Idempotency-Key", "e-1")
	if again := owner.must(200, "POST", "/v1/money/entries", exp, "Idempotency-Key", "e-1"); again["entry_id"] != first["entry_id"] {
		t.Errorf("retry %v", again)
	}
	owner.must(400, "POST", "/v1/money/entries", map[string]any{"kind": "cash", "amount_kobo": 500, "description": "float"})
	owner.must(201, "POST", "/v1/money/entries", map[string]any{"kind": "cash", "direction": "in", "amount_kobo": 500000, "description": "Till"})

	// Fee rates, and a leak closed as recovered.
	owner.must(400, "PUT", "/v1/money/fee-rates", map[string]any{"channel": "paystack/main", "percent_bp": 20000})
	owner.must(200, "PUT", "/v1/money/fee-rates", map[string]any{"channel": "paystack/main", "percent_bp": 150, "flat_kobo": 0, "cap_kobo": 200000})
	if r := viewer.must(200, "GET", "/v1/money/fee-rates", nil)["fee_rates"].([]any); len(r) != 1 {
		t.Fatalf("rates %v", r)
	}
	fee, amount := int64(300000), int64(50000000)
	if _, _, err := log.Record(ctx, tenant, moneylog.Entry{Direction: "in", Kind: "payment", Status: "settled", Channel: "paystack/main", Reference: "F-1",
		Event: "payment.received", AmountKobo: &amount, FeeKobo: &fee, Source: "event"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&leaks.Detector{Pool: w.env.DB.App, Now: time.Now}).Tenant(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	list := viewer.must(200, "GET", "/v1/money/leaks", nil)
	var feeLeak map[string]any
	for _, l := range list["leaks"].([]any) {
		if l.(map[string]any)["kind"] == "fee" {
			feeLeak = l.(map[string]any)
		}
	}
	if feeLeak == nil || feeLeak["amount_kobo"] != float64(100000) {
		t.Fatalf("leaks %v", list)
	}
	id := feeLeak["id"].(string)
	viewer.must(403, "POST", "/v1/money/leaks/"+id+"/close", map[string]any{"status": "written_off"})
	owner.must(400, "POST", "/v1/money/leaks/"+id+"/close", map[string]any{"status": "recovered"})
	owner.must(400, "POST", "/v1/money/leaks/"+id+"/assign", map[string]any{"member": "00000000-0000-0000-0000-000000000001"})
	owner.must(200, "POST", "/v1/money/leaks/"+id+"/assign", map[string]any{"member": nil})
	closed := owner.must(200, "POST", "/v1/money/leaks/"+id+"/close", map[string]any{"status": "recovered", "recovered_kobo": 100000, "note": "Paystack refunded it"})
	if closed["status"] != "recovered" {
		t.Fatalf("closed %v", closed)
	}
	owner.must(409, "POST", "/v1/money/leaks/"+id+"/close", map[string]any{"status": "written_off"})
	if tot := owner.must(200, "GET", "/v1/money/leaks?status=closed", nil)["totals"].(map[string]any); tot["recovered_kobo"] != float64(100000) {
		t.Errorf("totals %v", tot)
	}
	owner.must(400, "GET", "/v1/money/leaks?status=maybe", nil)
}
