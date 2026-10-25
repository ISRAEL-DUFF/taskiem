package schools_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/schools"
)

func (w *chaseWorld) clearance(student, term uuid.UUID) schools.Clearance {
	w.t.Helper()
	var out schools.Clearance
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = w.s.Clearance(ctx, tx, w.env.Tenant, student, term)
		return err
	})
	return out
}

func (w *chaseWorld) list(class *uuid.UUID) schools.ClearanceList {
	w.t.Helper()
	var out schools.ClearanceList
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = w.s.TermClearance(ctx, tx, w.env.Tenant, w.term.ID, class)
		return err
	})
	return out
}

func (w *chaseWorld) gate(student uuid.UUID) schools.GateAnswer {
	w.t.Helper()
	var out schools.GateAnswer
	w.in(func(tx pgx.Tx) error {
		var err error
		out, err = w.s.GateCheck(ctx, tx, w.env.Tenant, student)
		return err
	})
	return out
}

// The rule (docs/spec/schools.md 12.1): paid in full is cleared; some
// money and a balance is partly paid, never cleared; nothing is not paid;
// a plan in good standing clears, one in arrears does not; a student with
// no invoice is cleared only when their class has nothing required.
// Nothing is stored: a payment changes the answer at once.
func TestClearanceRule(t *testing.T) {
	w := newChaseWorld(t)
	tn, term := w.env.Tenant, w.term.ID
	for _, st := range []schools.StudentDetail{w.a, w.b, w.c} {
		if c := w.clearance(st.ID, term); c.Status != schools.NotPaid || c.Reason != schools.ReasonBalance || !c.Invoiced || c.BalanceKobo != 4_000_000 || c.PaidKobo != 0 {
			t.Fatalf("%s before paying: %+v", st.AdmissionNumber, c)
		}
	}
	// d has no family, so no invoice was sent: fees exist, not invoiced.
	if c := w.clearance(w.d.ID, term); c.Status != schools.NotPaid || c.Reason != schools.ReasonNoInvoice || c.Invoiced {
		t.Fatalf("d before paying: %+v", c)
	}
	w.pay("GCA/001", 4_000_000)
	w.pay("GCA/002", 3_999_900)
	if c := w.clearance(w.a.ID, term); c.Status != schools.Cleared || c.Reason != schools.ReasonPaid || c.BalanceKobo != 0 {
		t.Fatalf("paid in full: %+v", c)
	}
	if c := w.clearance(w.b.ID, term); c.Status != schools.PartlyPaid || c.Reason != schools.ReasonBalance || c.BalanceKobo != 100 || c.PaidKobo != 3_999_900 {
		t.Fatalf("one naira short is partly paid, never cleared: %+v", c)
	}
	if g := w.gate(w.a.ID); g.Status != schools.GateCleared || g.Name != "Ebube Eze" || g.Class != "JSS 1" {
		t.Fatalf("gate a: %+v", g)
	}
	if g := w.gate(w.b.ID); g.Status != schools.GateSeeBursar {
		t.Fatalf("gate b: %+v", g)
	}
	w.pay("GCA/002", 100)
	if c := w.clearance(w.b.ID, term); c.Status != schools.Cleared {
		t.Fatalf("the last naira clears: %+v", c)
	}

	// A plan approved for c, instalments ahead: cleared by the plan; an
	// instalment past and unpaid: in arrears, not cleared; settled: cleared.
	var req schools.PlanRequest
	w.in(func(tx pgx.Tx) error {
		var err error
		req, err = w.s.RequestPlan(ctx, tx, tn, w.other.ID, schools.PlanIn{StudentID: w.c.ID, Instalments: []schools.Instalment{{AmountKobo: 2_000_000, DueOn: "2026-09-30"}, {AmountKobo: 2_000_000, DueOn: "2026-10-31"}}})
		if err != nil {
			return err
		}
		_, err = w.s.DecidePlan(ctx, tx, tn, req.ID, schools.PlanDecision{Approve: true}, "user:bola", nil)
		return err
	})
	if c := w.clearance(w.c.ID, term); c.Status != schools.Cleared || c.Reason != schools.ReasonPlan || c.Plan == nil || c.Plan.State != schools.PlanGood || c.BalanceKobo != 4_000_000 {
		t.Fatalf("plan in good standing: %+v", c)
	}
	w.clock = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	if c := w.clearance(w.c.ID, term); c.Status != schools.NotPaid || c.Reason != schools.ReasonBalance || c.Plan.State != schools.PlanArrears {
		t.Fatalf("plan in arrears: %+v", c)
	}
	w.pay("GCA/003", 2_000_000)
	if c := w.clearance(w.c.ID, term); c.Status != schools.Cleared || c.Reason != schools.ReasonPlan {
		t.Fatalf("instalment paid: %+v", c)
	}

	// No invoice: a class with no required fees clears; a class with fees
	// and no invoice yet does not.
	art := w.class("Art club", "", 99)
	var e, f schools.StudentDetail
	w.in(func(tx pgx.Tx) error {
		var err error
		if e, err = w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Ife Art", AdmissionNumber: "GCA/005", ClassID: art.ID}, "user:ada"); err != nil {
			return err
		}
		f, err = w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Late Comer", AdmissionNumber: "GCA/006", ClassID: w.j1.ID}, "user:ada")
		return err
	})
	if c := w.clearance(e.ID, term); c.Status != schools.Cleared || c.Reason != schools.ReasonNoFees || c.Invoiced {
		t.Fatalf("nothing to pay: %+v", c)
	}
	if c := w.clearance(f.ID, term); c.Status != schools.NotPaid || c.Reason != schools.ReasonNoInvoice || c.Invoiced {
		t.Fatalf("fees but no invoice: %+v", c)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := w.s.SetFees(ctx, tx, tn, term, art.ID, []schools.FeeItemIn{{Name: "Paint", AmountKobo: 50_000, Kind: schools.Optional}}, "user:ada")
		return err
	})
	if c := w.clearance(e.ID, term); c.Status != schools.Cleared || c.Reason != schools.ReasonNoFees {
		t.Fatalf("optional items only still clear: %+v", c)
	}

	// The list: by class, with totals; of one class; a term the school
	// does not have.
	l := w.list(nil)
	if len(l.Rows) != 6 || l.Totals != (schools.ClearanceTotals{Students: 6, Cleared: 4, PartlyPaid: 0, NotPaid: 2}) || l.Term != "First term 2026/2027" {
		t.Fatalf("list %+v %+v", l.Totals, l.Rows)
	}
	if r := l.Rows[0]; r.AdmissionNumber != "GCA/001" || r.Name != "Ebube Eze" || r.Status != schools.Cleared || r.Class != "JSS 1" {
		t.Fatalf("first row %+v", r)
	}
	if l := w.list(&art.ID); len(l.Rows) != 1 || l.Rows[0].StudentID != e.ID {
		t.Fatalf("class list %+v", l.Rows)
	}
	if err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.TermClearance(ctx, tx, tn, uuid.New(), nil)
		return err
	}); !errors.Is(err, schools.ErrNotFound) {
		t.Fatalf("unknown term: %v", err)
	}
	// Summaries for teachers: counts and who is not cleared, no amounts.
	sum := schools.Summarise(l)
	if len(sum) != 2 || sum[0].Class != "JSS 1" || sum[0].Cleared != 3 || sum[0].NotPaid != 2 || len(sum[0].NotCleared) != 2 || !strings.Contains(sum[0].Text, "3 of 5 cleared") || !strings.Contains(sum[0].Text, "Nobody Known (GCA/004): not paid") || strings.Contains(sum[0].Text, "₦") {
		t.Fatalf("summary %+v", sum)
	}
	if !strings.HasSuffix(sum[1].Text, "Everyone is cleared.") {
		t.Fatalf("art summary %q", sum[1].Text)
	}
}

// Overrides (docs/spec/schools.md 12.2): a reason is required; the end
// date is the term's by default; one in force per student and term; past
// its end it no longer counts; withdrawn, it stops at once; nothing is
// stored on the student.
func TestClearanceOverrides(t *testing.T) {
	w := newChaseWorld(t)
	tn, term := w.env.Tenant, w.term.ID
	add := func(student string, in schools.OverrideIn) (schools.ClearanceOverride, error) {
		var o schools.ClearanceOverride
		err := w.try(func(tx pgx.Tx) error {
			var err error
			o, err = w.s.AddOverride(ctx, tx, tn, student, in, "user:bola")
			return err
		})
		return o, err
	}
	if _, err := add("GCA/004", schools.OverrideIn{}); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := add("GCA/004", schools.OverrideIn{Reason: "Bereavement", EndsOn: "2026-09-01"}); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("ends in the past: %v", err)
	}
	if _, err := add("GCA/009", schools.OverrideIn{Reason: "Bereavement"}); !errors.Is(err, schools.ErrNotFound) {
		t.Fatalf("unknown student: %v", err)
	}
	o, err := add("GCA/004", schools.OverrideIn{Reason: "Bereavement in the family"})
	if err != nil || o.StartsOn != "2026-09-17" || o.EndsOn != "2099-12-11" || !o.InForce || o.Reason != "Bereavement in the family" || o.TermID != term || o.CreatedBy != "user:bola" || o.AdmissionNumber != "GCA/004" {
		t.Fatalf("override %+v %v", o, err)
	}
	if c := w.clearance(w.d.ID, term); c.Status != schools.Cleared || c.Reason != schools.ReasonOverride || c.Override == nil || c.Override.ID != o.ID {
		t.Fatalf("overridden: %+v", c)
	}
	if g := w.gate(w.d.ID); g.Status != schools.GateCleared {
		t.Fatalf("gate: %+v", g)
	}
	if _, err := add("GCA/004", schools.OverrideIn{Reason: "Again"}); !errors.Is(err, schools.ErrState) {
		t.Fatalf("a second override in force: %v", err)
	}
	// An override ending on a date: counts through that day, not after.
	o2, err := add("GCA/003", schools.OverrideIn{Reason: "Exam week", EndsOn: "2026-09-20"})
	if err != nil || o2.EndsOn != "2026-09-20" {
		t.Fatalf("dated override %+v %v", o2, err)
	}
	w.clock = time.Date(2026, 9, 20, 20, 0, 0, 0, time.UTC)
	if c := w.clearance(w.c.ID, term); c.Status != schools.Cleared {
		t.Fatalf("on its last day: %+v", c)
	}
	w.clock = time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	if c := w.clearance(w.c.ID, term); c.Status != schools.NotPaid || c.Override != nil {
		t.Fatalf("the day after: %+v", c)
	}
	var list []schools.ClearanceOverride
	w.in(func(tx pgx.Tx) error {
		var err error
		list, err = w.s.Overrides(ctx, tx, tn, schools.OverrideFilter{Term: &term})
		return err
	})
	if len(list) != 2 || list[0].ID != o2.ID || list[0].InForce || !list[1].InForce {
		t.Fatalf("listed %+v", list)
	}
	w.in(func(tx pgx.Tx) error {
		var err error
		list, err = w.s.Overrides(ctx, tx, tn, schools.OverrideFilter{Term: &term, InForce: true})
		return err
	})
	if len(list) != 1 || list[0].ID != o.ID {
		t.Fatalf("in force %+v", list)
	}
	// Withdrawn, once; the student is not cleared again.
	w.in(func(tx pgx.Tx) error {
		got, err := w.s.WithdrawOverride(ctx, tx, tn, o.ID, "user:ada")
		if err == nil && (got.WithdrawnAt == nil || got.WithdrawnBy != "user:ada" || got.InForce) {
			t.Fatalf("withdrawn %+v", got)
		}
		return err
	})
	if err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.WithdrawOverride(ctx, tx, tn, o.ID, "user:ada")
		return err
	}); !errors.Is(err, schools.ErrState) {
		t.Fatalf("withdrawn twice: %v", err)
	}
	if c := w.clearance(w.d.ID, term); c.Status != schools.NotPaid || c.Override != nil {
		t.Fatalf("after withdrawing: %+v", c)
	}
	// There is no clearance column anywhere in the record.
	if n := w.count(`SELECT count(*) FROM information_schema.columns WHERE table_name LIKE 'school_%' AND column_name LIKE '%clear%'`); n != 0 {
		t.Fatalf("%d clearance columns", n)
	}
}

// The gate's search: three letters at least, over names only, active
// students, at most ten, those starting with the letters first.
func TestGateSearch(t *testing.T) {
	w := newChaseWorld(t)
	tn := w.env.Tenant
	search := func(q string) ([]schools.GateHit, error) {
		var out []schools.GateHit
		err := w.try(func(tx pgx.Tx) error {
			var err error
			out, err = w.s.GateSearch(ctx, tx, tn, q)
			return err
		})
		return out, err
	}
	if _, err := search("eb"); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("two letters: %v", err)
	}
	hits, err := search("EZE")
	if err != nil || len(hits) != 2 || hits[0].Name != "Ebube Eze" || hits[0].Class != "JSS 1" || hits[0].StudentID != w.a.ID {
		t.Fatalf("eze: %+v %v", hits, err)
	}
	if hits, _ := search("gca"); len(hits) != 0 {
		t.Fatalf("an admission number is not a name: %+v", hits)
	}
	if hits, _ := search("ade"); len(hits) != 1 || hits[0].Name != "Tolu Ade" {
		t.Fatalf("ade: %+v", hits)
	}
	w.in(func(tx pgx.Tx) error {
		st := schools.Withdrawn
		_, _, err := w.s.ChangeStudent(ctx, tx, tn, w.c.ID, schools.StudentChange{Status: &st}, "user:ada")
		return err
	})
	if hits, _ := search("ade"); len(hits) != 0 {
		t.Fatalf("withdrawn students are not at the gate: %+v", hits)
	}
	for i := range 12 {
		w.in(func(tx pgx.Tx) error {
			_, err := w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Abdul " + string(rune('A'+i)), AdmissionNumber: "ABD/" + string(rune('A'+i)), ClassID: w.j1.ID}, "user:ada")
			return err
		})
	}
	if hits, _ := search("abd"); len(hits) != schools.MaxGateHits {
		t.Fatalf("capped at %d: %d", schools.MaxGateHits, len(hits))
	}
	// A student the school does not have is not found; no current term
	// answers see the bursar.
	if err := w.try(func(tx pgx.Tx) error {
		_, err := w.s.GateCheck(ctx, tx, tn, uuid.New())
		return err
	}); !errors.Is(err, schools.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}
