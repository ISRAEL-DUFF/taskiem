package schools_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/schools"
)

type termWorld struct {
	*world
	j1, j2        schools.Class
	first, second schools.Term
	fam           schools.Family
}

// newTermWorld is a school with two classes, a session of two terms (the
// first started) and a family.
func newTermWorld(t *testing.T) *termWorld {
	w := &termWorld{world: newWorld(t)}
	tn := w.env.Tenant
	w.j1, w.j2 = w.class("JSS 1", "", 7), w.class("JSS 2", "", 8)
	w.in(func(tx pgx.Tx) error {
		sess, err := w.s.AddSession(ctx, tx, tn, "2026/2027", "user:ada")
		if err != nil {
			return err
		}
		if w.first, err = w.s.AddTerm(ctx, tx, tn, sess.ID, schools.TermIn{Name: "First term", StartsOn: "2026-09-14", EndsOn: "2099-12-11", FeesDueOn: "2099-09-07"}, "user:ada"); err != nil {
			return err
		}
		if w.second, err = w.s.AddTerm(ctx, tx, tn, sess.ID, schools.TermIn{Name: "Second term", StartsOn: "2100-01-11", EndsOn: "2100-04-02", FeesDueOn: "2100-01-04"}, "user:ada"); err != nil {
			return err
		}
		w.fam, err = w.s.AddFamily(ctx, tx, tn, []schools.GuardianIn{{Name: "Ngozi Eze", Phone: "08031234567", Email: "ngozi@example.com"}}, "user:ada")
		return err
	})
	return w
}

func (w *termWorld) student(name, number string, class schools.Class, family *uuid.UUID) schools.StudentDetail {
	w.t.Helper()
	var d schools.StudentDetail
	w.in(func(tx pgx.Tx) error {
		var err error
		d, err = w.s.AddStudent(ctx, tx, w.env.Tenant, schools.StudentIn{Name: name, AdmissionNumber: number, ClassID: class.ID, FamilyID: family}, "user:ada")
		return err
	})
	return d
}

func (w *termWorld) start(term schools.Term) {
	w.t.Helper()
	w.in(func(tx pgx.Tx) error {
		_, err := w.s.StartTerm(ctx, tx, w.env.Tenant, term.ID, "user:ada")
		return err
	})
}

func (w *termWorld) setFees(term schools.Term, class schools.Class, items []schools.FeeItemIn) schools.FeesSet {
	w.t.Helper()
	var out schools.FeesSet
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = w.s.SetFees(ctx, tx, w.env.Tenant, term.ID, class.ID, items, "user:ada")
		return err
	})
	return out
}

func (w *termWorld) plan() schools.TermPlan {
	w.t.Helper()
	var p schools.TermPlan
	w.in(func(tx pgx.Tx) error {
		var err error
		p, err = w.s.PlanTerm(ctx, tx, w.env.Tenant, w.first.ID)
		return err
	})
	return p
}

func (w *termWorld) invoice(hash string) schools.TermInvoiced {
	w.t.Helper()
	var out schools.TermInvoiced
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = w.s.InvoiceTerm(ctx, tx, w.env.Tenant, w.first.ID, hash, false, "user:ada")
		return err
	})
	return out
}

var j1Fees = []schools.FeeItemIn{
	{Name: "Tuition", AmountKobo: 10_000_000, Kind: schools.Required},
	{Name: "PTA levy", AmountKobo: 500_000},
	{Name: "Transport", AmountKobo: 2_000_000, Kind: schools.Optional},
	{Name: "WAEC fee", AmountKobo: 1_800_000, Kind: schools.Required, PassThroughFor: "WAEC"},
}

// A term is invoiced once per enrolled student, from the class's fees,
// their optional items and discounts; running it again invoices only
// the students added since; drafts follow the fees, sent invoices do not.
func TestInvoiceATerm(t *testing.T) {
	w := newTermWorld(t)
	tn := w.env.Tenant
	a := w.student("Ebube Eze", "GCA/001", w.j1, &w.fam.ID)
	b := w.student("Chidi Eze", "GCA/002", w.j1, &w.fam.ID)
	w.student("No Family", "GCA/003", w.j1, nil)
	w.student("Other Class", "GCA/004", w.j2, &w.fam.ID)
	w.start(w.first)

	set := w.setFees(w.first, w.j1, j1Fees)
	if len(set.Fees.Items) != 4 || set.Fees.RequiredKobo != 12_300_000 || set.Fees.PassThroughKobo != 1_800_000 || set.DraftsUpdated != 0 {
		t.Fatalf("fees %+v", set)
	}
	transport := set.Fees.Items[2]
	w.in(func(tx pgx.Tx) error {
		if _, _, err := w.s.SetOptions(ctx, tx, tn, a.ID, w.first.ID, []uuid.UUID{transport.ID}, "user:ada"); err != nil {
			return err
		}
		// A required item is not a choice.
		if _, _, err := w.s.SetOptions(ctx, tx, tn, a.ID, w.first.ID, []uuid.UUID{set.Fees.Items[0].ID}, "user:ada"); !errors.Is(err, schools.ErrInvalid) {
			t.Errorf("a required item chosen: %v", err)
		}
		ten := 10
		_, _, err := w.s.AddDiscount(ctx, tx, tn, b.ID, schools.DiscountIn{Kind: "sibling", Percent: &ten, Reason: "Second child"}, "user:ada")
		return err
	})

	p := w.plan()
	if p.Enrolled != 4 || p.ToInvoice != 2 || p.AlreadyInvoiced != 0 || len(p.NoContact) != 1 || len(p.NoFees) != 1 {
		t.Fatalf("plan %+v", p)
	}
	// Ebube: everything and transport; Chidi: 10% off the school's own
	// (tuition and PTA), never off WAEC's fee.
	if p.TotalKobo != 14_300_000+(12_300_000-1_050_000) || p.PassThroughKobo != 3_600_000 {
		t.Fatalf("plan total %d, pass-through %d", p.TotalKobo, p.PassThroughKobo)
	}
	if err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.InvoiceTerm(ctx, tx, tn, w.first.ID, "not-the-plan", false, "user:ada")
		return err
	}); !errors.Is(err, schools.ErrState) {
		t.Fatalf("another plan's hash: %v", err)
	}
	done := w.invoice(p.Hash)
	if done.Created != 2 || done.AlreadyInvoiced != 2 {
		t.Fatalf("invoiced %+v", done)
	}
	var list []schools.TermInvoice
	var inv invoicing.Invoice
	w.in(func(tx pgx.Tx) error {
		var err error
		if list, err = w.s.TermInvoices(ctx, tx, tn, w.first.ID, nil, ""); err != nil {
			return err
		}
		inv, err = w.s.Invoices.Get(ctx, tx, tn, list[1].InvoiceID.String())
		return err
	})
	if len(list) != 2 || list[0].AdmissionNumber != "GCA/001" || list[0].Name != "Ebube Eze" || list[0].Status != "draft" || list[0].Term != "First term 2026/2027" {
		t.Fatalf("term invoices %+v", list)
	}
	if inv.Customer.Key != schools.StudentKey(b.ID) || inv.CustomerID() != "key:"+schools.StudentKey(b.ID) || inv.Customer.Phone != "+2348031234567" ||
		inv.DueOn != "2099-09-07" || inv.TotalKobo != 11_250_000 || !strings.Contains(inv.Note, "GCA/002") || strings.Contains(inv.Note, "Chidi") {
		t.Fatalf("Chidi's invoice %+v", inv)
	}
	last := inv.Lines[len(inv.Lines)-1]
	if last.Kind != invoicing.LineDiscount || last.UnitKobo != -1_050_000 || last.Reason != "Second child" || inv.Lines[2].PassThroughFor != "WAEC" {
		t.Fatalf("lines %+v", inv.Lines)
	}

	// Again: nobody twice. A student added later is invoiced alone.
	if again := w.invoice(""); again.Created != 0 || again.AlreadyInvoiced != 2 {
		t.Fatalf("again %+v", again)
	}
	w.student("Late Comer", "GCA/005", w.j1, &w.fam.ID)
	if p := w.plan(); p.ToInvoice != 1 || p.AlreadyInvoiced != 2 {
		t.Fatalf("plan with one more %+v", p)
	}
	if again := w.invoice(""); again.Created != 1 {
		t.Fatalf("the late comer %+v", again)
	}
	if n := w.count(`SELECT count(*) FROM customer_invoices`); n != 3 {
		t.Fatalf("%d invoices", n)
	}

	// Send Ebube's; a change to the fees rebuilds the drafts only.
	if _, err := w.s.Invoices.Send(ctx, tn, list[0].Number, "user:ada", "k"); err != nil {
		t.Fatal(err)
	}
	changed := append([]schools.FeeItemIn{}, j1Fees...)
	changed[0].AmountKobo = 11_000_000
	if set := w.setFees(w.first, w.j1, changed); set.DraftsUpdated != 2 {
		t.Fatalf("drafts rebuilt %d", set.DraftsUpdated)
	}
	w.in(func(tx pgx.Tx) error {
		sent, err := w.s.Invoices.Get(ctx, tx, tn, list[0].Number)
		if err != nil {
			return err
		}
		draft, err := w.s.Invoices.Get(ctx, tx, tn, list[1].Number)
		if err != nil {
			return err
		}
		if sent.TotalKobo != 14_300_000 || draft.TotalKobo != 13_300_000-1_150_000 {
			t.Errorf("after the change: sent %d, draft %d", sent.TotalKobo, draft.TotalKobo)
		}
		return nil
	})

	// The step's key: one answer, however often the step runs; it sends.
	first, err := w.s.InvoiceTermOnce(ctx, tn, w.first.ID, "step-1", true, "workflow:fees")
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.s.InvoiceTermOnce(ctx, tn, w.first.ID, "step-1", true, "workflow:fees")
	if err != nil {
		t.Fatal(err)
	}
	if first.Created != 0 || first.Sent != 2 || second.Sent != 2 || second.Created != 0 {
		t.Fatalf("once: %+v then %+v", first, second)
	}
	if n := w.count(`SELECT count(*) FROM customer_invoices WHERE status = 'draft'`); n != 0 {
		t.Fatalf("%d drafts left", n)
	}
}

// A discount at or above the threshold needs a second person: refused
// until B5; a percent is weighed against the current term's fees.
func TestDiscountThreshold(t *testing.T) {
	w := newTermWorld(t)
	tn := w.env.Tenant
	a := w.student("Ebube Eze", "GCA/001", w.j1, &w.fam.ID)
	w.start(w.first)
	w.setFees(w.first, w.j1, j1Fees)
	w.in(func(tx pgx.Tx) error {
		_, err := schools.SetDiscountThreshold(ctx, tx, tn, 5_000_000, "user:ada")
		return err
	})
	big, half, small := int64(5_000_000), 50, 10
	for _, in := range []schools.DiscountIn{{Kind: "hardship", AmountKobo: &big, Reason: "x"}, {Kind: "scholarship", Percent: &half, Reason: "x"}} {
		err := w.try(func(tx pgx.Tx) error {
			_, _, err := w.s.AddDiscount(ctx, tx, tn, a.ID, in, "user:ada")
			return err
		})
		if !errors.Is(err, schools.ErrNeedsApproval) || !strings.Contains(schools.Plain(err), "approval") {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	w.in(func(tx pgx.Tx) error {
		_, _, err := w.s.AddDiscount(ctx, tx, tn, a.ID, schools.DiscountIn{Kind: "staff_child", Percent: &small, Reason: "Teacher's child"}, "user:ada")
		return err
	})
	// Off, nothing needs approval.
	w.in(func(tx pgx.Tx) error {
		if _, err := schools.SetDiscountThreshold(ctx, tx, tn, 0, "user:ada"); err != nil {
			return err
		}
		_, _, err := w.s.AddDiscount(ctx, tx, tn, a.ID, schools.DiscountIn{Kind: "hardship", AmountKobo: &big, Reason: "Hard year"}, "user:ada")
		return err
	})
}

// A past term's fees are read-only; a structure is copied from the term
// before.
func TestCopyAndPastFees(t *testing.T) {
	w := newTermWorld(t)
	tn := w.env.Tenant
	w.start(w.first)
	w.setFees(w.first, w.j1, j1Fees)
	w.start(w.second)
	err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.SetFees(ctx, tx, tn, w.first.ID, w.j1.ID, j1Fees[:1], "user:ada")
		return err
	})
	if !errors.Is(err, schools.ErrState) {
		t.Fatalf("a past term's fees changed: %v", err)
	}
	w.in(func(tx pgx.Tx) error {
		got, err := w.s.CopyFees(ctx, tx, tn, w.second.ID, w.j1.ID, nil, nil, "user:ada")
		if err != nil {
			return err
		}
		if len(got.Fees.Items) != 4 || got.Fees.Items[3].PassThroughFor != "WAEC" {
			t.Errorf("copied %+v", got)
		}
		other := w.j1.ID
		got, err = w.s.CopyFees(ctx, tx, tn, w.second.ID, w.j2.ID, &w.second.ID, &other, "user:ada")
		if err != nil || len(got.Fees.Items) != 4 {
			t.Errorf("from another class: %+v %v", got, err)
		}
		return nil
	})
}
