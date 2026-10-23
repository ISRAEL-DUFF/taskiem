package invoicing_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/sender"
)

type world struct {
	t   *testing.T
	env *rt.Env
	inv *invoicing.Invoices
}

func newWorld(t *testing.T) *world {
	env := rt.New(t)
	w := &world{t: t, env: env}
	w.inv = &invoicing.Invoices{Pool: env.DB.App, Vault: env.Vault, PageBase: "https://forms.test",
		Messages: &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: env.Store.DeliverSignalOnce}
	return w
}

func (w *world) in(fn func(pgx.Tx) error) {
	w.t.Helper()
	if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, fn); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) create(d invoicing.Draft) invoicing.Invoice {
	w.t.Helper()
	var x invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		x, err = w.inv.Create(context.Background(), tx, w.env.Tenant, d, "user:ada")
		return err
	})
	return x
}

var ada = invoicing.Customer{Name: "Ada Obi", Phone: "+2348031234567", Email: "ada@example.com"}

func TestNumbersStatusesAndPayments(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	x := w.create(invoicing.Draft{Customer: ada, Lines: []invoicing.Line{{Description: "Bags of rice", Quantity: 5, UnitKobo: 500000}}})
	if x.Number != "INV-0001" || x.Status != "draft" || x.TotalKobo != 2500000 || x.Customer.Name != "Ada Obi" {
		t.Fatalf("created %+v", x)
	}
	if y := w.create(invoicing.Draft{Customer: ada, AmountKobo: 100000}); y.Number != "INV-0002" || y.Lines[0].Description != "Amount due" {
		t.Fatalf("second %+v", y)
	}

	// A draft takes no payment.
	w.in(func(tx pgx.Tx) error {
		_, _, err := w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 100, PaymentID: "p0", How: "exact", By: "test"})
		if !errors.Is(err, invoicing.ErrState) {
			t.Errorf("draft payment: %v", err)
		}
		return nil
	})

	sent, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if sent.Invoice.Status != "sent" || !strings.HasPrefix(sent.Link, "https://forms.test/i/") || len(sent.Queued) != 2 {
		t.Fatalf("sent %+v", sent)
	}
	again, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k2")
	if err != nil || again.Link != sent.Link {
		t.Fatalf("resend %v: link %q, want %q", err, again.Link, sent.Link)
	}

	// The page finds it by its token, and shows nothing personal.
	tok := strings.TrimPrefix(sent.Link, "https://forms.test/i/")
	var number, status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT number, status FROM taskiem_public_customer_invoice($1)`, invoicing.TokenHash(tok)).Scan(&number, &status); err != nil || number != x.Number {
		t.Fatalf("page %v %q", err, number)
	}

	// Sent: what the customer was sent cannot change.
	w.in(func(tx pgx.Tx) error {
		_, err := w.inv.Update(ctx, tx, w.env.Tenant, x.Number, invoicing.Draft{Customer: ada, AmountKobo: 1}, "user:ada")
		if !errors.Is(err, invoicing.ErrState) {
			t.Errorf("update after send: %v", err)
		}
		return nil
	})
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE customer_invoices SET total_kobo = 1 WHERE number = $1`, x.Number)
		return err
	}); err == nil {
		t.Error("the database let a sent invoice's total change")
	}
	// Nobody may write a status or what was paid.
	for _, col := range []string{"status = 'paid'", "paid_kobo = 2500000"} {
		if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE customer_invoices SET `+col+` WHERE number = $1`, x.Number)
			return err
		}); err == nil {
			t.Errorf("the app role may set %s", col)
		}
	}

	// Payments: part-paid, then paid; the same payment counts once.
	var paid bool
	var p1 uuid.UUID
	w.in(func(tx pgx.Tx) error {
		y, became, err := w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 1000000, PaymentID: "pay-1", How: "exact", By: "test"})
		if err != nil {
			return err
		}
		if y.Status != "part_paid" || y.PaidKobo != 1000000 || y.DueKobo != 1500000 || became {
			t.Errorf("part %+v", y)
		}
		p1 = y.Payments[0].ID
		y, _, err = w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 1000000, PaymentID: "pay-1", How: "exact", By: "test"})
		if err != nil || y.PaidKobo != 1000000 {
			t.Errorf("repeat %v %+v", err, y)
		}
		y, paid, err = w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 1500000, PaymentID: "pay-2", How: "manual", By: "user:ada"})
		if err != nil || y.Status != "paid" || y.PaidAt == nil {
			t.Errorf("paid %v %+v", err, y)
		}
		return err
	})
	if !paid {
		t.Error("not reported as just paid")
	}
	// Undoing a payment moves it back, and keeps both rows.
	w.in(func(tx pgx.Tx) error {
		y, err := w.inv.UndoPayment(ctx, tx, w.env.Tenant, x.Number, p1, "user:ada")
		if err != nil {
			return err
		}
		undone := false
		for _, p := range y.Payments {
			undone = undone || (p.ID == p1 && p.UndoneBy != nil)
		}
		if y.Status != "part_paid" || y.PaidKobo != 1500000 || len(y.Payments) != 3 || !undone || y.PaidAt != nil {
			t.Errorf("undone %+v", y)
		}
		return nil
	})
	// Rows are never changed or deleted.
	for _, q := range []string{`UPDATE customer_invoice_payments SET amount_kobo = 1`, `DELETE FROM customer_invoice_payments`} {
		if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, q); return err }); err == nil {
			t.Errorf("the app role may %s", q)
		}
	}
}

func TestVoidOverdueAndSettings(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.in(func(tx pgx.Tx) error {
		_, err := invoicing.SaveSettings(ctx, tx, w.env.Tenant, invoicing.Settings{Prefix: "AK/", NextNumber: 100, Connection: "shop", FeePaidBy: "customer"})
		if err != nil {
			return err
		}
		if _, err := invoicing.SaveSettings(ctx, tx, w.env.Tenant, invoicing.Settings{Prefix: "AK/", NextNumber: 5, FeePaidBy: "business"}); !errors.Is(err, invoicing.ErrInvalid) {
			t.Errorf("number back: %v", err)
		}
		return nil
	})
	x := w.create(invoicing.Draft{Customer: invoicing.Customer{Name: "Bola", Email: "bola@example.com"}, AmountKobo: 50000, DueOn: "2020-01-01"})
	if x.Number != "AK/0100" || x.Connection != "shop" || x.FeePaidBy != "customer" {
		t.Fatalf("defaults %+v", x)
	}
	// Past its due date, it is overdue as soon as it is sent; the daily
	// check has nothing left to move.
	if s, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", ""); err != nil || s.Invoice.Status != "overdue" {
		t.Fatalf("sent %v %q", err, s.Invoice.Status)
	}
	if n, err := w.inv.Overdue(ctx); err != nil || n != 0 {
		t.Fatalf("overdue %d %v", n, err)
	}
	w.in(func(tx pgx.Tx) error {
		y, err := w.inv.Get(ctx, tx, w.env.Tenant, x.Number)
		if err != nil || y.Status != "overdue" {
			t.Errorf("status %v %q", err, y.Status)
		}
		if _, err := w.inv.Void(ctx, tx, w.env.Tenant, x.Number, "", "user:ada"); !errors.Is(err, invoicing.ErrInvalid) {
			t.Errorf("void without a reason: %v", err)
		}
		y, err = w.inv.Void(ctx, tx, w.env.Tenant, x.Number, "Raised in error", "user:ada")
		if err != nil || y.Status != "void" {
			t.Errorf("void %v %+v", err, y)
		}
		return nil
	})
	if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", ""); !errors.Is(err, invoicing.ErrState) {
		t.Errorf("sending a void invoice: %v", err)
	}
	// A void invoice counts no payment towards being paid.
	w.in(func(tx pgx.Tx) error {
		y, _, err := w.inv.RecordPayment(ctx, tx, w.env.Tenant, x.Number, invoicing.PaymentIn{AmountKobo: 50000, PaymentID: "late", How: "exact", By: "test"})
		if err != nil || y.Status != "void" {
			t.Errorf("payment to void %v %+v", err, y)
		}
		return nil
	})
}

func TestNumbersUnderConcurrency(t *testing.T) {
	w := newWorld(t)
	var mu sync.Mutex
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			x := w.create(invoicing.Draft{Customer: ada, AmountKobo: 1000})
			mu.Lock()
			defer mu.Unlock()
			if seen[x.Number] {
				t.Errorf("number %s given twice", x.Number)
			}
			seen[x.Number] = true
		})
	}
	wg.Wait()
	if len(seen) != 8 {
		t.Errorf("numbers %v", seen)
	}
}

func TestDraftChecks(t *testing.T) {
	w := newWorld(t)
	for name, d := range map[string]invoicing.Draft{
		"no name":         {Customer: invoicing.Customer{Phone: "+2348031234567"}, AmountKobo: 1000},
		"no address":      {Customer: invoicing.Customer{Name: "Ada"}, AmountKobo: 1000},
		"bad email":       {Customer: invoicing.Customer{Name: "Ada", Email: "ada"}, AmountKobo: 1000},
		"nothing to pay":  {Customer: ada},
		"both":            {Customer: ada, AmountKobo: 1000, Lines: []invoicing.Line{{Description: "x", Quantity: 1, UnitKobo: 100}}},
		"zero quantity":   {Customer: ada, Lines: []invoicing.Line{{Description: "x", Quantity: 0, UnitKobo: 100}}},
		"under a naira":   {Customer: ada, AmountKobo: 50},
		"bad due date":    {Customer: ada, AmountKobo: 1000, DueOn: "next week"},
		"fee paid by who": {Customer: ada, AmountKobo: 1000, FeePaidBy: "bank"},
	} {
		if err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			_, err := w.inv.Create(context.Background(), tx, w.env.Tenant, d, "user:ada")
			return err
		}); !errors.Is(err, invoicing.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNaira(t *testing.T) {
	for k, want := range map[int64]string{2500000: "₦25,000", 2500050: "₦25,000.50", 100: "₦1", 123456789: "₦1,234,567.89"} {
		if got := invoicing.Naira(k); got != want {
			t.Errorf("%d: %q, want %q", k, got, want)
		}
	}
}

// A customer who has had many messages this hour still gets the invoice
// sent: the throttled channel is left out, not the send refused.
func TestSendPastTheRecipientLimit(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	cust := invoicing.Customer{Name: "Ada Obi", Phone: "+2348031234567"}
	for i := range 7 {
		x := w.create(invoicing.Draft{Customer: cust, AmountKobo: 100000})
		sent, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k"+x.Number)
		if err != nil {
			t.Fatalf("send %d: %v", i+1, err)
		}
		if sent.Invoice.Status != "sent" {
			t.Fatalf("send %d: %+v", i+1, sent.Invoice)
		}
		if i >= 5 && len(sent.Queued) != 0 {
			t.Errorf("send %d queued %v past the limit", i+1, sent.Queued)
		}
	}
}
