package moneydigest_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/lang"
	"github.com/israel-duff/taskiem/engine/moneydigest"
	"github.com/israel-duff/taskiem/engine/moneyreport"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/schools"
	"github.com/israel-duff/taskiem/engine/sender"
)

// The schools section (B9): with the area on and a term current, the
// daily summary and the monthly email say what was collected for fees,
// what is outstanding and the share of students cleared, in every
// language; off, or with no record given, nothing is added.
func TestSchoolSection(t *testing.T) {
	e := rt.New(t)
	in := func(fn func(pgx.Tx) error) {
		t.Helper()
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, fn); err != nil {
			t.Fatal(err)
		}
	}
	inv := &invoicing.Invoices{Pool: e.DB.App, Vault: e.Vault, PageBase: "https://forms.test",
		Messages: &sender.Queue{Vault: e.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}}
	rec := &schools.Schools{Pool: e.DB.App, Vault: e.Vault, Invoices: inv}
	schools.WireReceipts(inv, rec)
	today, tomorrow := moneyreport.Day(time.Now())

	// Off: no section.
	var sum *moneydigest.SchoolSummary
	in(func(tx pgx.Tx) error {
		var err error
		sum, err = moneydigest.SchoolSummarise(ctx, tx, rec, e.Tenant, today, tomorrow)
		return err
	})
	if sum != nil {
		t.Fatalf("off: %+v", sum)
	}
	// A school with one student, the term invoiced and sent, ₦40,000 of
	// ₦100,000 paid in cash.
	var term schools.Term
	in(func(tx pgx.Tx) error {
		if _, err := schools.SetEnabled(ctx, tx, e.Tenant, true, "user:ada"); err != nil {
			return err
		}
		cl, err := rec.AddClass(ctx, tx, e.Tenant, schools.ClassIn{Name: "JSS 1", Order: 7}, "user:ada")
		if err != nil {
			return err
		}
		sess, err := rec.AddSession(ctx, tx, e.Tenant, "2026/2027", "user:ada")
		if err != nil {
			return err
		}
		if term, err = rec.AddTerm(ctx, tx, e.Tenant, sess.ID, schools.TermIn{Name: "First term", StartsOn: "2026-09-14", EndsOn: "2099-12-11", FeesDueOn: "2099-09-07"}, "user:ada"); err != nil {
			return err
		}
		fam, err := rec.AddFamily(ctx, tx, e.Tenant, []schools.GuardianIn{{Name: "Ngozi Eze", Phone: "08031234567"}}, "user:ada")
		if err != nil {
			return err
		}
		if _, err := rec.AddStudent(ctx, tx, e.Tenant, schools.StudentIn{Name: "Ebube Eze", AdmissionNumber: "GCA/001", ClassID: cl.ID, FamilyID: &fam.ID}, "user:ada"); err != nil {
			return err
		}
		if _, err := rec.StartTerm(ctx, tx, e.Tenant, term.ID, "user:ada"); err != nil {
			return err
		}
		if _, err := rec.SetFees(ctx, tx, e.Tenant, term.ID, cl.ID, []schools.FeeItemIn{{Name: "Tuition", AmountKobo: 10_000_000, Kind: schools.Required}}, "user:ada"); err != nil {
			return err
		}
		p, err := rec.PlanTerm(ctx, tx, e.Tenant, term.ID)
		if err != nil {
			return err
		}
		_, err = rec.InvoiceTerm(ctx, tx, e.Tenant, term.ID, p.Hash, false, "user:ada")
		return err
	})
	if _, failed, err := rec.SendTerm(ctx, e.Tenant, term.ID, "user:ada"); err != nil || len(failed) != 0 {
		t.Fatalf("sending the term: %v %v", err, failed)
	}
	if _, _, err := rec.PostPayment(ctx, e.Tenant, schools.PostingIn{Student: "GCA/001", AmountKobo: 4_000_000, How: "cash"}, "user:ada"); err != nil {
		t.Fatal(err)
	}
	in(func(tx pgx.Tx) error {
		var err error
		sum, err = moneydigest.SchoolSummarise(ctx, tx, rec, e.Tenant, today, tomorrow)
		return err
	})
	if sum == nil || sum.Term != "First term 2026/2027" || sum.CollectedKobo != 4_000_000 || sum.OutstandingKobo != 6_000_000 || sum.Students != 1 || sum.Cleared != 0 || sum.ClearedPercent != 0 {
		t.Fatalf("section %+v", sum)
	}
	// Yesterday's period: nothing collected then, the rest unchanged.
	var earlier *moneydigest.SchoolSummary
	in(func(tx pgx.Tx) error {
		var err error
		earlier, err = moneydigest.SchoolSummarise(ctx, tx, rec, e.Tenant, today.AddDate(0, 0, -1), today)
		return err
	})
	if earlier == nil || earlier.CollectedKobo != 0 || earlier.OutstandingKobo != 6_000_000 {
		t.Fatalf("yesterday %+v", earlier)
	}
	// No record given: no section.
	in(func(tx pgx.Tx) error {
		none, err := moneydigest.SchoolSummarise(ctx, tx, nil, e.Tenant, today, tomorrow)
		if none != nil {
			t.Errorf("without a record: %+v", none)
		}
		return err
	})

	// The words, in every language, after the money summary.
	var s moneyreport.Summary
	in(func(tx pgx.Tx) error {
		var err error
		s, err = moneyreport.Summarise(ctx, tx, e.Tenant, today, tomorrow)
		return err
	})
	text := moneydigest.DailyTextWith(lang.EN, s, sum, "https://app.test/money")
	line := "School fees (First term 2026/2027): ₦40,000 collected, ₦60,000 outstanding, 0% of students cleared."
	if !strings.Contains(text, "\n"+line+"\n\nMoney page: https://app.test/money") {
		t.Fatalf("daily text:\n%s", text)
	}
	if moneydigest.DailyTextWith(lang.EN, s, nil, "") != moneydigest.DailyText(lang.EN, s, "") {
		t.Error("without a section the text changed")
	}
	for _, l := range []lang.Tag{lang.YO, lang.HA, lang.IG, lang.PCM} {
		got := moneydigest.SchoolLine(l, sum)
		if strings.Contains(got, "{") || !strings.Contains(got, "₦40,000") || !strings.Contains(got, "₦60,000") || !strings.Contains(got, "0%") || !strings.Contains(got, "First term 2026/2027") {
			t.Errorf("%s: %q", l, got)
		}
	}
	_, body := moneydigest.MonthlyEmailWith("Greenfield", s, sum, "https://app.test/money")
	if !strings.Contains(body, "School fees (First term 2026/2027): ₦40,000 collected this month; ₦60,000 outstanding; 0 of 1 students cleared (0%).") {
		t.Fatalf("email:\n%s", body)
	}
	if strings.Contains(body, "Ebube") || strings.Contains(text, "Ebube") {
		t.Error("the summary names a student")
	}
	// Paying the rest clears the student: the share follows the money.
	if _, _, err := rec.PostPayment(ctx, e.Tenant, schools.PostingIn{Student: "GCA/001", AmountKobo: 6_000_000, How: "cash"}, "user:ada"); err != nil {
		t.Fatal(err)
	}
	in(func(tx pgx.Tx) error {
		var err error
		sum, err = moneydigest.SchoolSummarise(ctx, tx, rec, e.Tenant, today, tomorrow)
		return err
	})
	if sum.CollectedKobo != 10_000_000 || sum.OutstandingKobo != 0 || sum.ClearedPercent != 100 {
		t.Fatalf("paid %+v", sum)
	}
}
