package api_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/sender"
)

// The Money page's summary and the CSV exports over the API.
func TestMoneyReportsAPI(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	log := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
	owner := w.tenant(t, "Ada Foods", "ada@adafoods.test")
	viewer := addMember(t, w, owner, "vic", "viewer")
	tenant := tenantIDNamed(t, w, "Ada Foods")
	if _, err := log.RecordEvent(ctx, tenant, "prod", "paystack", "main", "payment.received", map[string]any{"payment_id": "=CMD()", "amount_kobo": int64(250000)}); err != nil {
		t.Fatal(err)
	}
	x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Bola Ade", "email": "bola@example.com"}, "amount_kobo": 900000})
	owner.must(200, "POST", "/v1/invoices/"+x["id"].(string)+"/send", nil, "Idempotency-Key", "s1")

	sum := viewer.must(200, "GET", "/v1/money/summary", nil)
	if sum["in_settled_kobo"] != float64(250000) || len(sum["channels"].([]any)) != 1 {
		t.Fatalf("summary %v", sum)
	}
	viewer.must(400, "GET", "/v1/money/summary?from=2026-10-31&to=2026-10-01", nil)
	viewer.must(400, "GET", "/v1/money/summary?month=October", nil)
	viewer.must(404, "GET", "/v1/money/exports/everything", nil)

	status, hdr, body := viewer.rawGet(t, "/v1/money/exports/log")
	if status != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") || !strings.Contains(hdr.Get("Content-Disposition"), "log-") {
		t.Fatalf("log export %d %v", status, hdr)
	}
	recs, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil || len(recs) != 2 || recs[1][9] != "'=CMD()" || recs[1][5] != "2500.00" {
		t.Fatalf("log %v %v", err, recs)
	}
	// Invoices: no names without reveal; reveal needs pii.reveal.
	if _, _, body := viewer.rawGet(t, "/v1/money/exports/invoices"); strings.Contains(body, "Bola") || !strings.Contains(body, x["number"].(string)) {
		t.Errorf("invoices %q", body)
	}
	if s, _, _ := viewer.rawGet(t, "/v1/money/exports/invoices?reveal=true"); s != 403 {
		t.Errorf("viewer reveal %d", s)
	}
	if _, _, body := owner.rawGet(t, "/v1/money/exports/invoices?reveal=true"); !strings.Contains(body, "Bola Ade") || strings.Contains(body, "bola@example.com") {
		t.Errorf("revealed %q", body)
	}
	if s, _, body := owner.rawGet(t, "/v1/money/exports/journal?month=2026-10"); s != 200 || !strings.HasPrefix(body, "date,journal_id,account,debit,credit") {
		t.Errorf("journal %d %q", s, body)
	}
}
