package invoicing_test

import (
	"context"
	"encoding/json"
	"errors"
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

// receiptWD starts on an invoice being paid.
const receiptWD = `{"schema":"wd/v1","id":"wf_receipt","version":1,"name":"Receipt","trigger":{"type":"invoice","config":{"event":"paid"}},
 "steps":[{"id":"t","type":"transform","config":{"output":{"number":"=trigger.body.number"}}}]}`

// deploy publishes receiptWD in a tenant and deploys it to envs (none: a
// version that was never deployed, as a draft is).
func (w *world) deploy(tenant uuid.UUID, envs ...string) uuid.UUID {
	w.t.Helper()
	wf := w.env.PublishIn(w.t, tenant, receiptWD)
	if len(envs) == 0 {
		return wf
	}
	def, err := wd.Load([]byte(receiptWD))
	if err != nil {
		w.t.Fatal(err)
	}
	ctx := context.Background()
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{tenant}, func(tx pgx.Tx) error {
		return ingest.SyncEnvironments(ctx, tx, tenant, wf, envs, 1, def, w.env.Registry, time.Now())
	}); err != nil {
		w.t.Fatal(err)
	}
	return wf
}

type started struct {
	id               uuid.UUID
	env, startedBy   string
	trigger          string
	tenant, workflow uuid.UUID
}

// runs lists every run of a workflow, in any tenant.
func (w *world) runs(wf uuid.UUID) []started {
	w.t.Helper()
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT r.id, r.environment, COALESCE(r.started_by, ''), e.payload::text, r.tenant_id, r.workflow_id
		FROM runs r JOIN run_events e ON e.run_id = r.id AND e.type = 'RunStarted' WHERE r.workflow_id = $1 ORDER BY r.started_at`, wf)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (started, error) {
		var s started
		err := r.Scan(&s.id, &s.env, &s.startedBy, &s.trigger, &s.tenant, &s.workflow)
		return s, err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func (w *world) startPaid() {
	w.t.Helper()
	if err := w.inv.StartPaid(context.Background()); err != nil {
		w.t.Fatal(err)
	}
}

// Paying an invoice starts each prod workflow of the tenant whose trigger
// is an invoice being paid, once: not another tenant's, not one deployed
// only to staging, not one never deployed; a replay, a second payment or
// the invoice paid again after an undo starts no second run.
func TestPaidInvoiceStartsWorkflowsOnce(t *testing.T) {
	w := newWorld(t)
	w.inv.Start = w.env.Store.Start
	ctx := context.Background()

	receipt := w.deploy(w.env.Tenant, "prod", "staging")
	staging := w.deploy(w.env.Tenant, "staging")
	draft := w.deploy(w.env.Tenant)
	other := w.env.AddTenant(t)
	theirs := w.deploy(other, "prod")

	x := w.create(invoicing.Draft{Customer: ada, AmountKobo: 2500000})
	if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k1"); err != nil {
		t.Fatal(err)
	}
	// Part paid: nothing starts.
	if err := w.inv.Received(ctx, w.env.Tenant, x.ID, invoicing.Received{PaymentID: "p1", AmountKobo: 1000000, Connection: "paystack/main"}); err != nil {
		t.Fatal(err)
	}
	w.startPaid()
	if got := w.runs(receipt); len(got) != 0 {
		t.Fatalf("part paid started %d runs", len(got))
	}
	// Paid in full (the payment checker's path): one run, in prod.
	if err := w.inv.Received(ctx, w.env.Tenant, x.ID, invoicing.Received{PaymentID: "p2", AmountKobo: 1500000, Connection: "paystack/main"}); err != nil {
		t.Fatal(err)
	}
	w.startPaid()
	got := w.runs(receipt)
	if len(got) != 1 || got[0].env != "prod" || got[0].startedBy != "invoice:"+x.Number || got[0].tenant != w.env.Tenant {
		t.Fatalf("runs %+v", got)
	}
	tr := got[0].trigger
	var payload struct {
		Trigger struct {
			Type string         `json:"type"`
			Body map[string]any `json:"body"`
		} `json:"trigger"`
	}
	if err := json.Unmarshal([]byte(tr), &payload); err != nil {
		t.Fatal(err)
	}
	b := payload.Trigger.Body
	if payload.Trigger.Type != "invoice" || b["id"] != x.ID.String() || b["number"] != x.Number || b["amount_kobo"] != 2500000.0 || b["paid_kobo"] != 2500000.0 ||
		b["currency"] != "NGN" || b["amount"] != "₦25,000" || b["paid_at"] == nil || b["link"] == nil {
		t.Errorf("trigger %s", tr)
	}
	if c, _ := b["customer"].(map[string]any); c == nil || c["name"] == nil || c["phone"] == nil || c["email"] == nil {
		t.Errorf("trigger's customer %v", b["customer"])
	}
	// The customer and the page link are sealed in the run's history.
	for _, secret := range []string{"Ada Obi", "+2348031234567", "ada@example.com", "https://forms.test/i/"} {
		if strings.Contains(tr, secret) {
			t.Errorf("trigger holds %q in plaintext: %s", secret, tr)
		}
	}
	// The same payment reported again, the drain run again, another
	// payment on the paid invoice: no second run.
	if err := w.inv.Received(ctx, w.env.Tenant, x.ID, invoicing.Received{PaymentID: "p2", AmountKobo: 1500000, Connection: "paystack/main"}); err != nil {
		t.Fatal(err)
	}
	if err := w.inv.Received(ctx, w.env.Tenant, x.ID, invoicing.Received{PaymentID: "p3", AmountKobo: 5000, Connection: "paystack/main"}); err != nil {
		t.Fatal(err)
	}
	w.startPaid()
	w.startPaid()
	// Undone and paid again: still one run.
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
		_, err := w.inv.UndoPayment(ctx, tx, w.env.Tenant, x.Number, p2, "user:ada")
		return err
	})
	w.in(func(tx pgx.Tx) error {
		_, became, err := w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 1500000, PaymentID: "p4", How: "manual", By: "user:ada"})
		if err == nil && !became {
			t.Error("paying again after the undo did not make it paid")
		}
		return err
	})
	w.startPaid()
	if got := w.runs(receipt); len(got) != 1 {
		t.Fatalf("%d runs after replays, another payment and an undo", len(got))
	}
	for name, wf := range map[string]uuid.UUID{"staging only": staging, "never deployed": draft, "another tenant's": theirs} {
		if got := w.runs(wf); len(got) != 0 {
			t.Errorf("the %s workflow started %d runs", name, len(got))
		}
	}
	// The outbox keeps the run it started.
	var run *uuid.UUID
	var done bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT run_id, done_at IS NOT NULL FROM invoice_paid_starts WHERE invoice_id = $1 AND workflow_id = $2`, x.ID, receipt).
		Scan(&run, &done); err != nil || run == nil || *run != got[0].id || !done {
		t.Fatalf("outbox row: %v %v %v", err, run, done)
	}
}

// The runs are queued in the payment's own transaction: a payment rolled
// back queues nothing, and a payment committed queues them whether or
// not anything starts them yet (a restart in between loses none).
func TestPaidStartsAreQueuedWithThePayment(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	receipt := w.deploy(w.env.Tenant, "prod")
	x := w.create(invoicing.Draft{Customer: ada, AmountKobo: 100000})
	if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k1"); err != nil {
		t.Fatal(err)
	}
	queued := func() int {
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM invoice_paid_starts WHERE invoice_id = $1`, x.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	rollback := context.Canceled
	err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		if _, _, err := w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 100000, PaymentID: "p1", How: "manual", By: "user:ada"}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) || queued() != 0 {
		t.Fatalf("rolled back: %v, %d queued", err, queued())
	}
	// Nothing to start them (Start unset): the row waits.
	w.in(func(tx pgx.Tx) error {
		_, _, err := w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 100000, PaymentID: "p1", How: "manual", By: "user:ada"})
		return err
	})
	w.startPaid()
	if queued() != 1 || len(w.runs(receipt)) != 0 {
		t.Fatalf("%d queued, %d runs", queued(), len(w.runs(receipt)))
	}
	// Started by the next server to drain.
	w.inv.Start = w.env.Store.Start
	w.startPaid()
	if got := w.runs(receipt); len(got) != 1 {
		t.Fatalf("%d runs", len(got))
	}
}

// A payment undone before its run could start starts none; paid again,
// the invoice starts its run then.
func TestPaidStartUndoneBeforeItRan(t *testing.T) {
	w := newWorld(t)
	w.inv.Start = w.env.Store.Start
	ctx := context.Background()
	receipt := w.deploy(w.env.Tenant, "prod")
	x := w.create(invoicing.Draft{Customer: ada, AmountKobo: 100000})
	if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k1"); err != nil {
		t.Fatal(err)
	}
	pay := func(id string) {
		w.in(func(tx pgx.Tx) error {
			_, _, err := w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 100000, PaymentID: id, How: "manual", By: "user:ada"})
			return err
		})
	}
	pay("p1")
	w.in(func(tx pgx.Tx) error {
		cur, err := w.inv.Get(ctx, tx, w.env.Tenant, x.Number)
		if err != nil {
			return err
		}
		_, err = w.inv.UndoPayment(ctx, tx, w.env.Tenant, x.Number, cur.Payments[0].ID, "user:ada")
		return err
	})
	w.startPaid()
	if got := w.runs(receipt); len(got) != 0 {
		t.Fatalf("%d runs for an invoice no longer paid", len(got))
	}
	pay("p2")
	w.startPaid()
	w.startPaid()
	if got := w.runs(receipt); len(got) != 1 {
		t.Fatalf("%d runs once paid again", len(got))
	}
}
