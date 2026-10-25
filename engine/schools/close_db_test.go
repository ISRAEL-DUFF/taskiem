package schools_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/schools"
)

// The Overview and the daily close (B9): the term's money as it stands,
// how it came and how it was matched, pass-through apart; a day closed
// exactly once, never recomputed, with what waits and what does not
// reconcile, each saying where to fix it.
func TestOverviewAndDailyClose(t *testing.T) {
	w := newCashWorld(t)
	tn := w.env.Tenant
	// Ebube pays ₦105,000 at the desk: own fees first (₦100,000), then
	// ₦5,000 of WAEC's; Chidi's family says it paid.
	if _, err := w.post("GCA/001", 10_500_000, "cash", "user:ada"); err != nil {
		t.Fatal(err)
	}
	w.in(func(tx pgx.Tx) error {
		_, err := w.s.RecordClaim(ctx, tx, tn, w.fam.ID, schools.ClaimIn{StudentID: w.b.ID, AmountKobo: 2_000_000, PaidOn: "2026-10-20", How: "transfer"}, "family")
		return err
	})
	var ov schools.Overview
	w.in(func(tx pgx.Tx) error {
		var err error
		ov, err = w.s.Overview(ctx, tx, tn, w.first.ID)
		return err
	})
	if ov.Term != "First term 2026/2027" || ov.Enrolled != 2 || ov.Invoiced != 2 || ov.ExpectedKobo != 23_600_000 || ov.CollectedKobo != 10_500_000 || ov.OutstandingKobo != 13_100_000 ||
		ov.ExpectedByNowKobo != 0 || ov.OverdueKobo != 0 || ov.CollectedPercent != 44 || ov.ThisWeekKobo != 10_500_000 {
		t.Fatalf("overview %+v", ov)
	}
	if ov.PassThroughKobo != 500_000 || len(ov.PassThrough) != 1 || ov.PassThrough[0].Body != "WAEC" {
		t.Fatalf("pass-through %+v", ov.PassThrough)
	}
	if ov.Matched.AutoKobo != 0 || ov.Matched.PersonKobo != 10_500_000 || ov.Matched.AutoPercent != 0 {
		t.Fatalf("matched %+v", ov.Matched)
	}
	if len(ov.HowPaid) != 1 || ov.HowPaid[0].How != schools.PaidByCash || ov.HowPaid[0].Payments != 1 || ov.HowPaid[0].Kobo != 10_500_000 {
		t.Fatalf("how paid %+v", ov.HowPaid)
	}
	if ov.Debtors != 2 || ov.DebtorsKobo != 13_100_000 || len(ov.TopDebtors) != 2 || ov.TopDebtors[0].AdmissionNumber != "GCA/002" || ov.Aging[schools.BucketNotDue].Students != 2 {
		t.Fatalf("debtors %+v %+v", ov.TopDebtors, ov.Aging)
	}
	if ov.Clearance.Students != 2 || ov.Clearance.PartlyPaid != 1 || ov.Clearance.NotPaid != 1 || ov.LastClose != nil {
		t.Fatalf("clearance %+v last %+v", ov.Clearance, ov.LastClose)
	}

	// Today is not over: no close of it. Yesterday (as of tomorrow) is.
	today := w.s.Yesterday().AddDate(0, 0, 1)
	err := w.try(func(tx pgx.Tx) error {
		_, _, err := w.s.CloseDay(ctx, tx, tn, today, "user:ada")
		return err
	})
	if !errors.Is(err, schools.ErrState) {
		t.Fatalf("closing today: %v", err)
	}
	w.s.Now = func() time.Time { return time.Now().Add(24 * time.Hour) }
	var c schools.Close
	var fresh bool
	w.in(func(tx pgx.Tx) error {
		var err error
		c, fresh, err = w.s.CloseDay(ctx, tx, tn, today, "user:ada")
		return err
	})
	if !fresh || c.Day != today.Format(time.DateOnly) || c.RanBy != "user:ada" || c.Term != "First term 2026/2027" {
		t.Fatalf("close %+v", c)
	}
	if c.MoneyIn.Count != 1 || c.MoneyIn.Kobo != 10_500_000 || c.AtTheDesk.Kobo != 10_500_000 || c.IntoAccounts.Count != 0 || c.LookbackFound.Count != 0 {
		t.Fatalf("money in %+v", c)
	}
	if c.MatchedPerson.Kobo != 10_500_000 || c.MatchedAuto.Count != 0 || c.Unmatched.Count != 0 || len(c.Pending) != 0 {
		t.Fatalf("matched %+v", c)
	}
	if len(c.Claims) != 1 || c.Claims[0].Kind != schools.ItemClaim || c.Claims[0].Ref != "GCA/002" || c.Claims[0].Path != schools.PathToMatch || !strings.Contains(c.Claims[0].Message, "₦20,000") {
		t.Fatalf("claims %+v", c.Claims)
	}
	if !c.Reconciles || c.Problems != 0 || len(c.ProblemList) != 0 {
		t.Fatalf("reconciles %+v", c)
	}
	text := schools.CloseText(c, "https://app.test")
	for _, want := range []string{"Daily close, ", "Money in on students: ₦105,000 (1 payments)", "₦105,000 at the desk", "Everything reconciles", "Claims:", "https://app.test/invoices/match"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Ebube") || strings.Contains(text, "Chidi") {
		t.Error("the close names a student")
	}
	// Taken once: the same day again is the stored close, whoever asks,
	// whatever happened since.
	if _, err := w.post("GCA/002", 1_000_000, "cash", "user:ada"); err != nil {
		t.Fatal(err)
	}
	var again schools.Close
	w.in(func(tx pgx.Tx) error {
		var err error
		again, fresh, err = w.s.CloseDay(ctx, tx, tn, today, "platform")
		return err
	})
	if fresh || again.ID != c.ID || again.RanBy != "user:ada" || again.MoneyIn.Kobo != 10_500_000 {
		t.Fatalf("second close %+v", again)
	}
	var list []schools.CloseBrief
	w.in(func(tx pgx.Tx) error {
		var err error
		list, err = w.s.Closes(ctx, tx, tn, "", "", 0)
		return err
	})
	if len(list) != 1 || list[0].ID != c.ID || !list[0].Reconciles {
		t.Fatalf("closes %+v", list)
	}
	w.in(func(tx pgx.Tx) error {
		var err error
		ov, err = w.s.Overview(ctx, tx, tn, w.first.ID)
		return err
	})
	if ov.LastClose == nil || ov.LastClose.ID != c.ID {
		t.Fatalf("last close %+v", ov.LastClose)
	}
	// The nightly run closes every school's yesterday once.
	if r, err := w.s.CloseYesterday(ctx); err != nil || r.Schools < 1 || r.Taken != 0 {
		t.Fatalf("nightly: %+v %v", r, err)
	}
	// A close is never changed.
	if err := w.try(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE school_daily_closes SET reconciles = false WHERE id = $1`, c.ID)
		return err
	}); err == nil {
		t.Fatal("a close was changed")
	}
}

// A payment counted with no receipt behind it does not reconcile: the
// close says so, with the invoice to fix it on.
func TestDailyCloseFlagsWhatDoesNotReconcile(t *testing.T) {
	w := newTermWorld(t) // receipts are not wired here
	tn := w.env.Tenant
	w.student("Ebube Eze", "GCA/001", w.j1, &w.fam.ID)
	w.start(w.first)
	w.setFees(w.first, w.j1, []schools.FeeItemIn{{Name: "Tuition", AmountKobo: 10_000_000, Kind: schools.Required}})
	w.invoice(w.plan().Hash)
	if _, failed, err := w.s.SendTerm(ctx, tn, w.first.ID, "user:ada"); err != nil || len(failed) != 0 {
		t.Fatalf("sending the term: %v %v", err, failed)
	}
	if _, _, err := w.s.PostPayment(ctx, tn, schools.PostingIn{Student: "GCA/001", AmountKobo: 4_000_000, How: "pos"}, "user:ada"); err != nil {
		t.Fatal(err)
	}
	w.s.Now = func() time.Time { return time.Now().Add(24 * time.Hour) }
	var c schools.Close
	w.in(func(tx pgx.Tx) error {
		var err error
		c, _, err = w.s.CloseDay(ctx, tx, tn, w.s.Yesterday(), "platform")
		return err
	})
	if c.Reconciles || c.Problems != 1 || c.ProblemList[0].Kind != schools.ProblemPartWithoutReceipt || !strings.HasPrefix(c.ProblemList[0].Path, "/invoices/") || c.ProblemList[0].Ref == "" {
		t.Fatalf("close %+v", c)
	}
	if c.AtTheDesk.Kobo != 4_000_000 || c.MatchedPerson.Kobo != 4_000_000 {
		t.Fatalf("desk %+v", c)
	}
	text := schools.CloseText(c, "")
	if !strings.Contains(text, "DOES NOT RECONCILE: 1 problem(s)") || !strings.Contains(text, "has no receipt") {
		t.Fatalf("text:\n%s", text)
	}
	// By the connector's wrapper, a day in words; a bad day refused.
	svc := schools.Service{Schools: w.s}
	if _, err := svc.CloseFor(ctx, tn, "soon", "workflow:x"); !errors.Is(err, schools.ErrInvalid) {
		t.Fatalf("bad day: %v", err)
	}
	if got, err := svc.CloseFor(ctx, tn, "", "workflow:x"); err != nil || got.ID != c.ID {
		t.Fatalf("by default yesterday: %+v %v", got, err)
	}
}
