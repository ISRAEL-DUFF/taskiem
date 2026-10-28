package matching_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
)

var ctx = context.Background()

type world struct {
	t   *testing.T
	env *rt.Env
	inv *invoicing.Invoices
	log *moneylog.Log
	m   *matching.Matcher
}

func newWorld(t *testing.T) *world {
	env := rt.New(t)
	log := &moneylog.Log{Pool: env.DB.App, PII: env.Vault}
	inv := &invoicing.Invoices{Pool: env.DB.App, Vault: env.Vault, PageBase: "https://forms.test", MoneyLog: log,
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: env.Store.DeliverSignalOnce}
	return &world{t: t, env: env, inv: inv, log: log, m: &matching.Matcher{Invoices: inv, PII: env.Vault}}
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) invoice(name string, kobo int64) invoicing.Invoice {
	w.t.Helper()
	var x invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		x, err = w.inv.Create(ctx, tx, w.env.Tenant, invoicing.Draft{Customer: invoicing.Customer{Name: name, Phone: "+2348031234567"}, AmountKobo: kobo}, "user:ada")
		return err
	})
	if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "send:"+x.Number); err != nil {
		w.t.Fatal(err)
	}
	return x
}

func (w *world) read(number string) invoicing.Invoice {
	w.t.Helper()
	x, err := (invoicing.Service{Invoices: w.inv}).Read(ctx, w.env.Tenant, number)
	if err != nil {
		w.t.Fatal(err)
	}
	return x
}

func (w *world) heard(event string, fields map[string]any) {
	w.t.Helper()
	if event == "virtual_account.credited" && fields["credit_id"] == nil {
		fields["credit_id"] = "C-" + fields["reference"].(string) // each its own credit
	}
	if _, err := w.log.RecordEvent(ctx, w.env.Tenant, "prod", "paystack", "main", event, fields); err != nil {
		w.t.Fatal(err)
	}
	if err := w.m.Tick(ctx); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) open() []matching.Waiting {
	w.t.Helper()
	var out []matching.Waiting
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = matching.Open(ctx, tx, w.env.Tenant, 50)
		return err
	})
	return out
}

// A payment into the one-time account Taskiem gave marks its invoice paid
// with no person involved.
func TestExactMatch(t *testing.T) {
	w := newWorld(t)
	x := w.invoice("Ada Obi", 2500000)
	w.in(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO customer_invoice_accounts (id, tenant_id, invoice_id, seq, kind, connector, connection, payment_id, amount_kobo, status)
			VALUES (gen_random_uuid(), $1, $2, 1, 'transfer', 'paystack', 'main', 'tsk_acct_1', 2500000, 'open')`, w.env.Tenant, x.ID)
		return err
	})
	w.heard("payment.received", map[string]any{"payment_id": "tsk_acct_1", "amount_kobo": int64(2500000)})
	y := w.read(x.Number)
	if y.Status != "paid" || len(y.Payments) != 1 || y.Payments[0].How != "exact" {
		t.Fatalf("after the payment %+v", y)
	}
	if len(w.open()) != 0 {
		t.Error("an exact match left suggestions")
	}
	// Heard again (polled as well as pushed): nothing more.
	w.heard("payment.received", map[string]any{"payment_id": "tsk_acct_1", "amount_kobo": int64(2500000)})
	if y := w.read(x.Number); len(y.Payments) != 1 {
		t.Errorf("payments %+v", y.Payments)
	}
}

// An unreferenced transfer gets a suggestion with its reasons; confirmed,
// it pays the invoice; undone, the invoice moves back, both rows stay, and
// the wrong suggestion is not offered again.
func TestSuggestConfirmUndo(t *testing.T) {
	w := newWorld(t)
	x := w.invoice("Ada Obi", 2500000)
	other := w.invoice("Bola Ade", 900000)
	w.heard("virtual_account.credited", map[string]any{"reference": "VA-77", "amount_kobo": int64(2500000), "payer_name": "OBI ADA C"})

	open := w.open()
	if len(open) != 1 || open[0].Number != x.Number || open[0].Score < matching.Auto {
		t.Fatalf("suggestions %+v", open)
	}
	why := open[0].Reasons
	if len(why) < 3 || why[0] != "same amount as what is due on "+x.Number+" (₦25,000)" || why[1] != "the payer's name matches the customer's" {
		t.Errorf("reasons %q", why)
	}
	// Automatic matching is off: nothing was matched.
	if y := w.read(x.Number); y.Status != "sent" {
		t.Fatalf("matched without asking: %+v", y)
	}

	res, err := w.m.Confirm(ctx, w.env.Tenant, open[0].ID, "user:ada", false)
	if err != nil || res.Invoice == nil || res.Target() != "invoice:"+x.Number {
		t.Fatalf("confirmed %v %+v", err, res)
	}
	y := *res.Invoice
	if y.Status != "paid" || y.Payments[0].How != "suggested" {
		t.Fatalf("confirmed %+v", y)
	}
	// The same money cannot count towards a second invoice.
	if _, err := w.m.Match(ctx, w.env.Tenant, open[0].EntryID, other.Number, "user:ada"); err == nil {
		t.Error("one payment counted twice")
	}
	var undone invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		undone, err = w.inv.UndoPayment(ctx, tx, w.env.Tenant, x.Number, y.Payments[0].ID, "user:ada")
		return err
	})
	if undone.Status != "sent" || len(undone.Payments) != 2 {
		t.Fatalf("undone %+v", undone)
	}
	if len(w.open()) != 0 {
		t.Error("the undone suggestion is offered again")
	}
	// Now matched by hand to the right invoice... which is the other one
	// in this story only if a person says so.
	res, err = w.m.Match(ctx, w.env.Tenant, open[0].EntryID, x.Number, "user:ada")
	if err != nil || res.Invoice == nil {
		t.Fatalf("manual %v %+v", err, res)
	}
	if z := *res.Invoice; z.Status != "paid" || z.Payments[len(z.Payments)-1].How != "manual" {
		t.Fatalf("manual %+v", z)
	}
}

// Rejected suggestions are not offered again; with automatic matching on,
// a strong and clear one is matched alone, an unclear one is not.
func TestRejectAndAutoMatch(t *testing.T) {
	w := newWorld(t)
	// Suggested hears of what a person is asked, not of automatic matches.
	var told [][]matching.Suggestion
	w.m.Suggested = func(_ context.Context, tenant, entry uuid.UUID, top []matching.Suggestion) {
		for _, s := range top {
			if tenant != w.env.Tenant || s.EntryID != entry || s.ID == uuid.Nil || s.Status != "open" {
				t.Errorf("told %s %s %+v", tenant, entry, s)
			}
		}
		told = append(told, top)
	}
	a := w.invoice("Ada Obi", 2500000)
	w.heard("payment.received", map[string]any{"payment_id": "PAY-1", "amount_kobo": int64(2500000)})
	open := w.open()
	if len(open) != 1 || open[0].Number != a.Number {
		t.Fatalf("suggestions %+v", open)
	}
	if len(told) != 1 || len(told[0]) != 1 || told[0][0].ID != open[0].ID {
		t.Fatalf("told %+v", told)
	}
	if err := w.m.Reject(ctx, w.env.Tenant, open[0].ID, "user:ada"); err != nil {
		t.Fatal(err)
	}
	if err := w.m.Tick(ctx); err != nil || len(w.open()) != 0 {
		t.Fatalf("rejected comes back: %v %+v", err, w.open())
	}

	w.in(func(tx pgx.Tx) error {
		st, err := invoicing.GetSettings(ctx, tx, w.env.Tenant)
		if err != nil {
			return err
		}
		st.AutoMatch = true
		_, err = invoicing.SaveSettings(ctx, tx, w.env.Tenant, st)
		return err
	})
	b := w.invoice("Chidi Eze", 700000)
	w.heard("virtual_account.credited", map[string]any{"reference": "VA-1", "amount_kobo": int64(700000), "payer_name": "CHIDI EZE"})
	if y := w.read(b.Number); y.Status != "paid" || y.Payments[0].How != "suggested_auto" || len(y.Payments[0].Reasons) == 0 {
		t.Fatalf("auto %+v", y)
	}
	if len(told) != 1 {
		t.Errorf("told of an automatic match: %+v", told)
	}
	// Two invoices of the same amount and no name: not clear, so asked.
	c1, c2 := w.invoice("Dayo", 400000), w.invoice("Efe", 400000)
	w.heard("payment.received", map[string]any{"payment_id": "PAY-2", "amount_kobo": int64(400000)})
	if w.read(c1.Number).Status != "sent" || w.read(c2.Number).Status != "sent" || len(w.open()) != 2 {
		t.Errorf("unclear auto: %s %s %+v", w.read(c1.Number).Status, w.read(c2.Number).Status, w.open())
	}
	if len(told) != 2 || len(told[1]) != 2 || told[1][0].Score < told[1][1].Score {
		t.Errorf("told of the unclear one: %+v", told)
	}
}

func TestScore(t *testing.T) {
	sent := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	x := invoicing.Invoice{ID: uuid.New(), Number: "INV-0104", DueKobo: 500000, SentAt: &sent, Customer: invoicing.Customer{Name: "Ada Obi"}}
	cases := []struct {
		amount     int64
		payer, ref string
		when       time.Time
		ok         bool
		score      int
	}{
		{500000, "ADA OBI", "", sent.Add(48 * time.Hour), true, 100},
		{500000, "", "", sent.Add(48 * time.Hour), true, 65},
		{100, "", "Payment for inv-0104", sent.Add(30 * 24 * time.Hour), true, 45},
		{100, "ADA OKAFOR", "", sent.Add(24 * time.Hour), false, 0},
		{500000, "", "", sent.Add(-48 * time.Hour), false, 0},
	}
	for i, c := range cases {
		s, ok := matching.Score(c.amount, c.payer, c.ref, c.when, x)
		if ok != c.ok || ok && s.Score != c.score {
			t.Errorf("%d: %v %d %q", i, ok, s.Score, s.Reasons)
		}
	}
}
