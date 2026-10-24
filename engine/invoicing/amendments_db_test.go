package invoicing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
)

func (w *world) amend(number string, in invoicing.AmendmentIn) (invoicing.Amended, error) {
	var out invoicing.Amended
	err := db.InTenantTx(context.Background(), w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
		var err error
		out, err = w.inv.Amend(context.Background(), tx, w.env.Tenant, number, in, "user:ada")
		return err
	})
	return out, err
}

func (w *world) pay(number, id string, kobo int64) invoicing.Invoice {
	w.t.Helper()
	var x invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		x, _, err = w.inv.RecordPayment(context.Background(), tx, w.env.Tenant, number, invoicing.PaymentIn{AmountKobo: kobo, PaymentID: id, How: "manual", By: "user:ada"})
		return err
	})
	return x
}

// A sent invoice changes only by amendment: its own line with a reason,
// audited; the total and status follow from the lines and payments; the
// total never goes below what was paid; a paid invoice whose total rises
// is part-paid again.
func TestAmendments(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	x := w.create(invoicing.Draft{Customer: ada, Lines: []invoicing.Line{
		{Description: "Tuition", Quantity: 1, UnitKobo: 10_000_000},
		{Description: "WAEC fee", Quantity: 1, UnitKobo: 2_000_000, PassThroughFor: "WAEC"},
	}})
	if _, err := w.amend(x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendAdd, Description: "Books", AmountKobo: 100_000, Reason: "x"}); !errors.Is(err, invoicing.ErrState) {
		t.Fatalf("a draft amended: %v", err)
	}
	if _, err := w.inv.Send(ctx, w.env.Tenant, x.Number, "user:ada", "k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.amend(x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendAdd, Description: "Books", AmountKobo: 100_000}); !errors.Is(err, invoicing.ErrInvalid) {
		t.Fatalf("no reason: %v", err)
	}
	a, err := w.amend(x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendAdd, Description: "Books", AmountKobo: 500_000, Reason: "Books for the term"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Invoice.TotalKobo != 12_500_000 || len(a.Invoice.Lines) != 3 || a.Invoice.Lines[2].Amendment != a.Amendment.ID.String() || len(a.Invoice.Amendments) != 1 {
		t.Fatalf("added %+v", a)
	}
	// A discount on what is the business's own only.
	if _, err := w.amend(x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendDiscount, AmountKobo: 11_000_000, Reason: "too much"}); !errors.Is(err, invoicing.ErrState) {
		t.Fatalf("a discount into WAEC's fee: %v", err)
	}
	one := 1
	if _, err := w.amend(x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendRemove, Line: &one, Reason: "Not sitting WAEC"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.amend(x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendRemove, Line: &one, Reason: "again"}); !errors.Is(err, invoicing.ErrState) {
		t.Fatalf("a line taken out twice: %v", err)
	}
	// Paid 10m of 10.5m; a discount of 1m would go below it.
	y := w.pay(x.Number, "p1", 10_000_000)
	if y.Status != "part_paid" || y.TotalKobo != 10_500_000 {
		t.Fatalf("part paid %+v", y)
	}
	if _, err := w.amend(x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendDiscount, AmountKobo: 1_000_000, Reason: "Hardship"}); !errors.Is(err, invoicing.ErrState) {
		t.Fatalf("below what was paid: %v", err)
	}
	d, err := w.amend(x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendDiscount, AmountKobo: 500_000, Description: "Hardship discount", Reason: "Hardship"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Invoice.Status != "paid" || !d.Became {
		t.Fatalf("a discount that pays it %+v", d.Invoice)
	}
	// Paid, then more added: part-paid again.
	m, err := w.amend(x.Number, invoicing.AmendmentIn{Kind: invoicing.AmendAdd, Description: "Lab fee", AmountKobo: 200_000, Reason: "Lab"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Invoice.Status != "part_paid" || m.Invoice.PaidAt != nil || m.Invoice.DueKobo != 200_000 {
		t.Fatalf("paid then amended %+v", m.Invoice)
	}
	// The database refuses a line that is no recorded amendment, and an
	// amendment's line added twice.
	for _, q := range []string{
		`UPDATE customer_invoices SET lines = lines || '[{"description":"Sneaky","quantity":1,"unit_kobo":100}]'::jsonb WHERE number = $1`,
		`UPDATE customer_invoices SET lines = lines || jsonb_build_array(lines->2) WHERE number = $1`,
		`UPDATE customer_invoices SET total_kobo = 1 WHERE number = $1`,
	} {
		if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{w.env.Tenant}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, q, x.Number)
			return err
		}); err == nil {
			t.Errorf("the database let %s", q)
		}
	}
	var audited int
	w.in(func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'invoice.amended' AND target = $1`, x.Number).Scan(&audited)
	})
	if audited != 4 {
		t.Errorf("%d amendments audited", audited)
	}
}
