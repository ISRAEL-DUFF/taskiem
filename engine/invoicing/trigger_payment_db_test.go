package invoicing_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/wd"
)

// paymentWD starts on every payment counted towards an invoice.
const paymentWD = `{"schema":"wd/v1","id":"wf_payment","version":1,"name":"Payment receipt","trigger":{"type":"invoice","config":{"event":"payment"}},
 "steps":[{"id":"t","type":"transform","config":{"output":{"number":"=trigger.body.number"}}}]}`

// deployPayment publishes paymentWD in a tenant and deploys it to prod.
func (w *world) deployPayment(tenant uuid.UUID) uuid.UUID {
	w.t.Helper()
	wf := w.env.PublishIn(w.t, tenant, paymentWD)
	def, err := wd.Load([]byte(paymentWD))
	if err != nil {
		w.t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(ctx, tx, tenant, wf, []string{"prod"}, 1, def, w.env.Registry, time.Now())
	}); err != nil {
		w.t.Fatal(err)
	}
	return wf
}

func (w *world) startPayments() {
	w.t.Helper()
	if err := w.inv.StartPayments(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

// Every part counted towards an invoice starts each prod workflow whose
// invoice trigger is the payment event, once per part: a part paid in
// full or not, a replay of the same payment no second time, and a part
// undone before its run could start none. A workflow on paid is not
// started by a part payment, and one on payment not by paid.
func TestPaymentsStartWorkflowsOncePerPart(t *testing.T) {
	w := newWorld(t)
	w.inv.Start = w.env.Store.Start
	ctx := context.Background()
	payment := w.deployPayment(w.env.Tenant)
	paid := w.deploy(w.env.Tenant, "prod")

	x := w.create(invoicing.Draft{Customer: ada, AmountKobo: 2500000})
	if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k1"); err != nil {
		t.Fatal(err)
	}
	// A part: one run, with the payment and what is still due.
	if err := w.inv.Received(ctx, w.env.Tenant, x.ID, invoicing.Received{PaymentID: "p1", AmountKobo: 1000000, Connection: "paystack/main"}); err != nil {
		t.Fatal(err)
	}
	w.startPayments()
	w.startPaid()
	got := w.runs(payment)
	if len(got) != 1 || got[0].env != "prod" || got[0].startedBy != "invoice:"+x.Number {
		t.Fatalf("runs %+v", got)
	}
	if n := w.runs(paid); len(n) != 0 {
		t.Fatalf("a part payment started %d runs on paid", len(n))
	}
	var payload struct {
		Trigger struct {
			Type, Event string
			Body        map[string]any `json:"body"`
		} `json:"trigger"`
	}
	if err := json.Unmarshal([]byte(got[0].trigger), &payload); err != nil {
		t.Fatal(err)
	}
	b := payload.Trigger.Body
	pm, _ := b["payment"].(map[string]any)
	if payload.Trigger.Type != "invoice" || payload.Trigger.Event != "payment" || b["event"] != "payment" || b["status"] != "part_paid" || b["due_kobo"] != 1500000.0 ||
		b["paid_kobo"] != 1000000.0 || b["number"] != x.Number || pm == nil || pm["amount_kobo"] != 1000000.0 || pm["amount"] != "₦10,000" || pm["how"] != "exact" || pm["id"] == nil || pm["paid_at"] == nil {
		t.Errorf("trigger %s", got[0].trigger)
	}
	if _, ok := b["receipt"]; ok {
		t.Errorf("a receipt for an invoice that is no school's: %v", b["receipt"])
	}
	if strings.Contains(got[0].trigger, "Ada Obi") || strings.Contains(got[0].trigger, "https://forms.test/i/") {
		t.Errorf("trigger holds personal data in plaintext: %s", got[0].trigger)
	}
	// The same payment again: no second run. A second part: a second run,
	// and paid in full starts the paid workflow too.
	if err := w.inv.Received(ctx, w.env.Tenant, x.ID, invoicing.Received{PaymentID: "p1", AmountKobo: 1000000, Connection: "paystack/main"}); err != nil {
		t.Fatal(err)
	}
	w.startPayments()
	if got := w.runs(payment); len(got) != 1 {
		t.Fatalf("a replay started %d runs", len(got))
	}
	if err := w.inv.Received(ctx, w.env.Tenant, x.ID, invoicing.Received{PaymentID: "p2", AmountKobo: 1500000, Connection: "paystack/main"}); err != nil {
		t.Fatal(err)
	}
	w.startPayments()
	w.startPaid()
	if got := w.runs(payment); len(got) != 2 {
		t.Fatalf("%d runs after the second part", len(got))
	}
	if got := w.runs(paid); len(got) != 1 {
		t.Fatalf("%d runs on paid", len(got))
	}
	// A part undone before the drain runs starts nothing; the row says why.
	var p2 uuid.UUID
	w.in(func(tx pgx.Tx) error {
		cur, err := w.inv.Get(ctx, tx, w.env.Tenant, x.Number)
		for _, p := range cur.Payments {
			if p.PaymentID == "p2" {
				p2 = p.ID
			}
		}
		return err
	})
	w.in(func(tx pgx.Tx) error {
		if _, err := w.inv.UndoPayment(ctx, tx, w.env.Tenant, x.Number, p2, "user:ada"); err != nil {
			return err
		}
		_, _, err := w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 1500000, PaymentID: "p3", How: "manual", By: "user:ada"})
		return err
	})
	var p3 uuid.UUID
	w.in(func(tx pgx.Tx) error {
		cur, err := w.inv.Get(ctx, tx, w.env.Tenant, x.Number)
		for _, p := range cur.Payments {
			if p.PaymentID == "p3" {
				p3 = p.ID
			}
		}
		return err
	})
	w.in(func(tx pgx.Tx) error {
		_, err := w.inv.UndoPayment(ctx, tx, w.env.Tenant, x.Number, p3, "user:ada")
		return err
	})
	w.startPayments()
	if got := w.runs(payment); len(got) != 2 {
		t.Fatalf("%d runs after an undone part", len(got))
	}
	var note *string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT note FROM invoice_payment_starts WHERE part_id = $1 AND workflow_id = $2`, p3, payment).Scan(&note); err != nil || note == nil || !strings.Contains(*note, "undone") {
		t.Fatalf("undone part's row: %v %v", err, note)
	}
	var open int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM invoice_payment_starts WHERE done_at IS NULL`).Scan(&open); err != nil || open != 0 {
		t.Fatalf("%d starts still open (%v)", open, err)
	}
}
