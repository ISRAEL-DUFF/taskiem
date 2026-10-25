package demo_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/matching"
	"github.com/israel-duff/taskiem/engine/moneylog"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/schools/demo"
	"github.com/israel-duff/taskiem/engine/sender"
)

var ctx = context.Background()

type world struct {
	t      *testing.T
	env    *rt.Env
	s      *schools.Schools
	seeder *demo.Seeder
}

// newWorld wires the demo as the CLI does: the schools service on the demo
// rail, receipts, the money log and a matcher that voids receipts on a
// reversal, and a message queue the demo holds its guardians on.
func newWorld(t *testing.T) *world {
	env := rt.New(t)
	q := &sender.Queue{Vault: env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}
	inv := &invoicing.Invoices{Pool: env.DB.App, Vault: env.Vault, PageBase: "https://forms.test", Messages: q, MoneyLog: &moneylog.Log{Pool: env.DB.App, PII: env.Vault}}
	s := &schools.Schools{Pool: env.DB.App, Vault: env.Vault, Invoices: inv, Rails: demo.Rails{}}
	schools.WireReceipts(inv, s)
	m := &matching.Matcher{Invoices: inv, PII: env.Vault, Reversed: s.ReceiptsReversed}
	s.Matcher = m
	if _, err := env.DB.Admin.Exec(ctx, `UPDATE tenants SET name = 'Demo School' WHERE id = $1`, env.Tenant); err != nil {
		t.Fatal(err)
	}
	return &world{t: t, env: env, s: s, seeder: &demo.Seeder{Schools: s, MoneyLog: inv.MoneyLog, Matcher: m, Messages: q}}
}

func (w *world) in(fn func(tx pgx.Tx) error) {
	w.t.Helper()
	if err := w.s.In(ctx, w.env.Tenant, fn); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) count(q string, args ...any) int {
	w.t.Helper()
	var n int
	w.in(func(tx pgx.Tx) error { return tx.QueryRow(ctx, q, args...).Scan(&n) })
	return n
}

var opts = demo.Options{Owner: "user:owner", Bursar: "user:bursar", Approver: "user:approver"}

// The demo seeds a whole school into a fresh tenant: the counts, the
// Overview's totals, every kind of exception a bursar meets, yesterday's
// close reconciling with what waits listed; seeding again does nothing.
func TestSeedADemoSchool(t *testing.T) {
	w := newWorld(t)
	tn := w.env.Tenant
	o := opts
	o.Tenant = tn
	res, err := w.seeder.Seed(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Seeded || res.Rail != demo.Channel || res.Classes != 6 || res.Families != 25 || res.Students != 40 || res.Invoiced != 40 || res.AccountsIssued != 40 || !res.Reconciles || res.CloseDay == "" {
		t.Fatalf("result %+v", res)
	}
	if n := w.count(`SELECT count(*) FROM school_students WHERE tenant_id = $1 AND admission_number LIKE 'DEMO/%'`, tn); n != 40 {
		t.Errorf("%d students", n)
	}
	if n := w.count(`SELECT count(*) FROM school_families WHERE tenant_id = $1`, tn); n != 25 {
		t.Errorf("%d families", n)
	}
	if n := w.count(`SELECT count(*) FROM customer_accounts WHERE tenant_id = $1 AND channel = $2`, tn, demo.Channel); n != 40 {
		t.Errorf("%d accounts", n)
	}

	// The Overview: every student invoiced; most of the money came into
	// the accounts by itself, some was posted by a person; the pass-through
	// kept apart; the clearance as designed.
	var ov schools.Overview
	w.in(func(tx pgx.Tx) error {
		var err error
		ov, err = w.s.Overview(ctx, tx, tn, res.TermID)
		return err
	})
	if ov.Enrolled != 40 || ov.Invoiced != 40 || ov.ExpectedKobo <= 0 || ov.CollectedKobo <= 0 || ov.OutstandingKobo != ov.ExpectedKobo-ov.CollectedKobo || ov.OverdueKobo <= 0 {
		t.Fatalf("overview %+v", ov)
	}
	if ov.Matched.AutoKobo <= ov.Matched.PersonKobo || ov.Matched.AutoPercent < 60 {
		t.Errorf("matched %+v", ov.Matched)
	}
	if ov.PassThroughKobo <= 0 || len(ov.PassThrough) != 1 || ov.PassThrough[0].Body != "NECO" {
		t.Errorf("pass-through %+v", ov.PassThrough)
	}
	hows := map[string]int{}
	for _, h := range ov.HowPaid {
		hows[h.How] = h.Payments
	}
	if hows[schools.PaidByAccount] < 28 || hows[schools.PaidByCash] != 1 || hows[schools.PaidByTransfer] != 1 || hows[schools.PaidByCard] != 1 {
		t.Errorf("how paid %v", hows)
	}
	if ov.Clearance != (schools.ClearanceTotals{Students: 40, Cleared: 25, PartlyPaid: 7, NotPaid: 8}) {
		t.Errorf("clearance %+v", ov.Clearance)
	}
	if ov.LastClose == nil || ov.LastClose.Day != res.CloseDay || !ov.LastClose.Reconciles || len(ov.TopDebtors) != 5 {
		t.Errorf("last close %+v, %d top debtors", ov.LastClose, len(ov.TopDebtors))
	}

	// The exceptions: what waits for a second person, who says they paid,
	// the credit held with its reason, the voided receipt on the reopened
	// invoice, the promise, the plans.
	w.in(func(tx pgx.Tx) error {
		pending, err := w.s.Postings(ctx, tx, tn, schools.PostingFilter{Status: schools.StatePending})
		if err != nil {
			return err
		}
		kinds := map[string]int{}
		for _, p := range pending {
			kinds[p.Kind]++
			if p.RequestedBy != "user:bursar" {
				t.Errorf("request %s by %q", p.Kind, p.RequestedBy)
			}
		}
		if kinds[schools.KindCashPosting] != 1 || kinds[schools.KindDiscount] != 1 || len(pending) != 2 {
			t.Errorf("pending %v", kinds)
		}
		claims, err := w.s.Claims(ctx, tx, tn, schools.ClaimFilter{Status: "open"})
		if err != nil || len(claims) != 2 {
			t.Errorf("claims %d %v", len(claims), err)
		}
		held, err := matching.Unmatched(ctx, tx, tn, 50)
		if err != nil || len(held) != 1 || held[0].HeldReason != matching.HeldOtherAccount {
			t.Errorf("unmatched %+v %v", held, err)
		}
		var reversedStudent uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM school_students WHERE tenant_id = $1 AND admission_number = 'DEMO/024'`, tn).Scan(&reversedStudent); err != nil {
			return err
		}
		receipts, err := w.s.Receipts(ctx, tx, tn, schools.ReceiptFilter{Student: &reversedStudent})
		if err != nil || len(receipts) != 1 || !receipts[0].Voided {
			t.Errorf("the reversed student's receipts %+v %v", receipts, err)
		}
		cl, err := w.s.Clearance(ctx, tx, tn, reversedStudent, res.TermID)
		if err != nil || cl.Status != schools.NotPaid || cl.PaidKobo != 0 {
			t.Errorf("the reversed student's clearance %+v %v", cl, err)
		}
		for has, want := range map[string]int{"promise": 1, "plan": 1, "arrears": 1} {
			d, err := w.s.Debtors(ctx, tx, tn, schools.DebtorFilter{Has: has})
			if err != nil || len(d.Debtors) != want {
				t.Errorf("debtors with %s: %d %v", has, len(d.Debtors), err)
			}
		}
		overrides, err := w.s.Overrides(ctx, tx, tn, schools.OverrideFilter{})
		if err != nil || len(overrides) != 1 {
			t.Errorf("overrides %d %v", len(overrides), err)
		}
		if n := w.count(`SELECT count(*) FROM school_receipts WHERE tenant_id = $1 AND voided_at IS NULL`, tn); n < 30 {
			t.Errorf("%d valid receipts", n)
		}
		// Nothing was queued to the demo's numbers.
		if n := w.count(`SELECT count(*) FROM platform_messages WHERE tenant_id = $1`, tn); n != 0 {
			t.Errorf("%d messages queued", n)
		}
		return nil
	})

	// Yesterday's close: money came in, into the accounts and at the desk
	// (the matches are counted on the day they were made, today); the
	// cash posting and the discount wait; the two claims are listed; it
	// reconciles.
	var c schools.Close
	w.in(func(tx pgx.Tx) error {
		var ok bool
		var err error
		c, ok, err = w.s.CloseOf(ctx, tx, tn, w.s.Yesterday())
		if err == nil && !ok {
			err = errors.New("no close of yesterday")
		}
		return err
	})
	if c.MoneyIn.Count != 8 || c.IntoAccounts.Count != 6 || c.AtTheDesk.Count != 2 || c.MoneyIn.Kobo != c.IntoAccounts.Kobo+c.AtTheDesk.Kobo {
		t.Errorf("close money %+v %+v %+v", c.MoneyIn, c.IntoAccounts, c.AtTheDesk)
	}
	if len(c.Pending) != 2 || len(c.Claims) != 2 || !c.Reconciles || c.Problems != 0 {
		t.Errorf("close %+v", c)
	}

	// Seeding again changes nothing.
	again, err := w.seeder.Seed(ctx, o)
	if err != nil || again.Seeded {
		t.Fatalf("again: %+v %v", again, err)
	}
	if n := w.count(`SELECT count(*) FROM school_students WHERE tenant_id = $1`, tn); n != 40 {
		t.Errorf("%d students after seeding again", n)
	}
	if n := w.count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1`, tn); n != 34 {
		t.Errorf("%d money entries after seeding again", n)
	}
}

// A tenant with students of its own is never seeded over.
func TestSeedRefusesASchoolsOwnStudents(t *testing.T) {
	w := newWorld(t)
	tn := w.env.Tenant
	w.in(func(tx pgx.Tx) error {
		if _, err := schools.SetEnabled(ctx, tx, tn, true, "user:ada"); err != nil {
			return err
		}
		c, err := w.s.AddClass(ctx, tx, tn, schools.ClassIn{Name: "JSS 1", Order: 7}, "user:ada")
		if err != nil {
			return err
		}
		_, err = w.s.AddStudent(ctx, tx, tn, schools.StudentIn{Name: "Ebube Eze", AdmissionNumber: "GCA/001", ClassID: c.ID}, "user:ada")
		return err
	})
	o := opts
	o.Tenant = tn
	if _, err := w.seeder.Seed(ctx, o); !errors.Is(err, demo.ErrOtherStudents) {
		t.Fatalf("seeded over a school: %v", err)
	}
	if n := w.count(`SELECT count(*) FROM school_students WHERE tenant_id = $1`, tn); n != 1 {
		t.Errorf("%d students", n)
	}
}
