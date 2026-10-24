package matching_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/wd"
)

// An invoice paid by a match, confirmed from a suggestion or made by
// hand, starts the workflows that start on an invoice being paid, once
// per invoice (the trigger is queued by invoicing.RecordPayment, which
// every match goes through).
func TestMatchedInvoiceStartsItsWorkflowOnce(t *testing.T) {
	w := newWorld(t)
	w.inv.Start = w.env.Store.Start
	const receiptWD = `{"schema":"wd/v1","id":"wf_receipt","version":1,"name":"Receipt","trigger":{"type":"invoice","config":{"event":"paid"}},
	 "steps":[{"id":"t","type":"transform","config":{"output":{"number":"=trigger.body.number"}}}]}`
	wf := w.env.Publish(t, receiptWD)
	def, err := wd.Load([]byte(receiptWD))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(ctx, tx, w.env.Tenant, wf, []string{"prod"}, 1, def, w.env.Registry, time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	runsFor := func(number string) int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE workflow_id = $1 AND started_by = $2`, wf, "invoice:"+number).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	drain := func() {
		t.Helper()
		for range 2 {
			if err := w.inv.StartPaid(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Confirmed from a suggestion.
	x := w.invoice("Ada Obi", 2500000)
	w.heard("virtual_account.credited", map[string]any{"reference": "VA-77", "amount_kobo": int64(2500000), "payer_name": "OBI ADA C"})
	open := w.open()
	if len(open) != 1 || open[0].Number != x.Number {
		t.Fatalf("suggestions %+v", open)
	}
	if y, err := w.m.Confirm(ctx, w.env.Tenant, open[0].ID, "user:ada", false); err != nil || y.Invoice == nil || y.Invoice.Status != "paid" {
		t.Fatalf("confirmed %v %+v", err, y)
	}
	if _, err := w.m.Confirm(ctx, w.env.Tenant, open[0].ID, "user:ada", false); err == nil {
		t.Error("a suggestion confirmed twice")
	}
	drain()
	if n := runsFor(x.Number); n != 1 {
		t.Fatalf("%s: %d runs", x.Number, n)
	}

	// Matched by hand; the same match again changes nothing.
	z := w.invoice("Bola Ade", 900000)
	w.heard("payment.received", map[string]any{"payment_id": "PAY-9", "amount_kobo": int64(900000)})
	var entry uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM money_entries WHERE tenant_id = $1 AND reference = 'PAY-9'`, w.env.Tenant).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if y, err := w.m.Match(ctx, w.env.Tenant, entry, z.Number, "user:ada"); err != nil || y.Invoice == nil || y.Invoice.Status != "paid" {
			t.Fatalf("matched %v %+v", err, y)
		}
	}
	drain()
	if n := runsFor(z.Number); n != 1 {
		t.Fatalf("%s: %d runs", z.Number, n)
	}
	if n := runsFor(x.Number); n != 1 {
		t.Fatalf("%s: %d runs after another invoice was paid", x.Number, n)
	}
}
