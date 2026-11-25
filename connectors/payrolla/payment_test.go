package payrolla_test

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
)

// The routes after locking are Payrolla's, with its error codes.
func TestFakePaymentFollowsTheOpenAPIFile(t *testing.T) {
	raw, err := os.ReadFile("../../docs/integrations/payrolla-reference/api/payrolla-connect-v1-2026-11-24.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	for _, r := range payrollatest.PaymentRoutes() {
		method, path, _ := strings.Cut(r, " ")
		block := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(path) + `:\n((?:    .*\n|\n)*)`).FindStringSubmatch(spec)
		if block == nil || !strings.Contains(block[1], "    "+strings.ToLower(method)+":") {
			t.Errorf("the fake serves %s, which Payrolla's OpenAPI file does not have", r)
		}
	}
	for _, c := range payrollatest.PaymentErrorCodes() {
		if !strings.Contains(spec, c) {
			t.Errorf("the fake answers %s, which Payrolla's OpenAPI file does not name", c)
		}
	}
}

// lockedRun prepares, approves (Musa) and locks (Ngozi) November's run.
func lockedRun(t *testing.T, f *payrollatest.Fake, cl *payrolla.Client, ngozi, musa uuid.UUID) string {
	t.Helper()
	ctx := context.Background()
	w := func(who uuid.UUID, key string) payrolla.Write {
		return payrolla.Write{Key: key, User: who, Name: "Someone"}
	}
	got, err := cl.CreateRun(ctx, payrolla.Period{Period: "2026-11"}, w(ngozi, "prep"))
	if err != nil {
		t.Fatal(err)
	}
	run := got.Run.ID
	for _, step := range []struct {
		who    uuid.UUID
		user   string
		action string
	}{{musa, musaID, payrolla.ActionApprove}, {ngozi, ngoziID, payrolla.ActionLock}} {
		ch, err := cl.StartRunConfirmation(ctx, run, step.action, w(step.who, "c-"+step.action))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cl.Decide(ctx, run, step.action, ch.ID, f.Code(step.user), w(step.who, "d-"+step.action)); err != nil {
			t.Fatal(err)
		}
	}
	return run
}

// A locked run's payment: scheduled, then waiting for money on the pay
// date, paid after a top-up with one payout failing, sent again with a
// code; pay now and paying again with each refusal; payslips final only
// once locked, and an employee's own.
func TestPaymentAndPayslips(t *testing.T) {
	f, cl := runWorld(t)
	ctx := context.Background()
	ngozi, musa := uuid.New(), uuid.New()
	link(t, f, cl, ngoziID, ngozi)
	link(t, f, cl, musaID, musa)
	w := func(who uuid.UUID, key string) payrolla.Write {
		return payrolla.Write{Key: key, User: who, Name: "Someone"}
	}

	// Before locking: no payslip yet.
	draft, err := cl.CreateRun(ctx, payrolla.Period{Period: "2026-10"}, w(ngozi, "oct"))
	if err != nil {
		t.Fatal(err)
	}
	lines, _ := cl.Lines(ctx, draft.Run.ID)
	if _, err := cl.Payslip(ctx, lines[0].PayslipID); payrolla.Code(err) != "PAYSLIP_NOT_FINAL" {
		t.Fatalf("not final: %v", err)
	}
	if p, err := cl.RunPayment(ctx, draft.Run.ID); err != nil || p.Status != "NOT_SCHEDULED" {
		t.Fatalf("not scheduled: %+v %v", p, err)
	}

	run := lockedRun(t, f, cl, ngozi, musa)
	if p, err := cl.RunPayment(ctx, run); err != nil || p.Status != "SCHEDULED" || p.ScheduledFor != "2026-11-30" || p.Pending != 3 {
		t.Fatalf("scheduled: %+v %v", p, err)
	}
	// Nothing failed yet; Musa may not pay.
	if _, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionRetryFailed, w(ngozi, "r0")); payrolla.Code(err) != "INVALID_STATE" {
		t.Fatalf("nothing to retry: %v", err)
	}
	if _, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionPayNow, w(musa, "p0")); payrolla.Code(err) != "CANNOT_PAY" {
		t.Fatalf("cannot pay: %v", err)
	}

	// The pay date with a short wallet: waiting for money; pay now refused.
	f.Runs().WalletKobo = 100
	f.PayDay(run)
	if p, _ := cl.RunPayment(ctx, run); p.Status != "AWAITING_FUNDS" {
		t.Fatalf("awaiting: %+v", p)
	}
	if q := f.Queued(); q[len(q)-1] != "run.awaiting_funds" {
		t.Fatalf("events: %v", q)
	}
	if _, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionPayNow, w(ngozi, "p1")); payrolla.Code(err) != "AWAITING_FUNDS" {
		t.Fatalf("pay now while short: %v", err)
	}

	// Topped up: paid, Musa Ali's account closed.
	f.FailPayout(lines[1].EmployeeID, "Account closed")
	f.TopUp(200000000)
	p, _ := cl.RunPayment(ctx, run)
	if p.Status != "PARTIALLY_COMPLETED" || p.Paid != 2 || p.Failed != 1 {
		t.Fatalf("paid: %+v", p)
	}
	if _, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionPayNow, w(ngozi, "p2")); payrolla.Code(err) != "ALREADY_PAYING" {
		t.Fatalf("already paying: %v", err)
	}
	ch, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionRetryFailed, w(ngozi, "r1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Decide(ctx, run, payrolla.ActionRetryFailed, ch.ID, "000000", w(ngozi, "r2")); payrolla.Code(err) != "CODE_INVALID" {
		t.Fatalf("wrong code: %v", err)
	}
	d, err := cl.Decide(ctx, run, payrolla.ActionRetryFailed, ch.ID, f.Code(ngoziID), w(ngozi, "r3"))
	if err != nil || d.Payment.Status != "COMPLETED" || d.Payment.Paid != 3 {
		t.Fatalf("sent again: %+v %v", d, err)
	}
	var issued int
	for _, q := range f.Queued() {
		if q == "payslip.issued" {
			issued++
		}
	}
	if issued != 3 {
		t.Fatalf("payslips issued %d: %v", issued, f.Queued())
	}

	// Payslips as data, and an employee's own, newest first.
	now, _ := cl.Lines(ctx, run)
	ps, err := cl.Payslip(ctx, now[0].PayslipID)
	if err != nil || ps.Employer.RCNumber == "" || ps.NetKobo != now[0].NetKobo || len(ps.Items) < 5 || ps.PaymentStatus != "CONFIRMED" || ps.Employee.FirstName != "Zainab" {
		t.Fatalf("payslip: %+v %v", ps, err)
	}
	mine, err := cl.EmployeePayslips(ctx, now[0].EmployeeID)
	if err != nil || len(mine) != 1 || mine[0].ID != now[0].PayslipID || mine[0].Period != "2026-11" {
		t.Fatalf("mine: %+v %v", mine, err)
	}
	link, err := cl.CreatePayslipLink(ctx, ps.ID, w(ngozi, "link"))
	if err != nil || !strings.HasPrefix(link.URL, "https://") {
		t.Fatalf("link: %+v %v", link, err)
	}

	// A returned payment is not sent again.
	f.Reverse(run, now[2].EmployeeID)
	if p, _ := cl.RunPayment(ctx, run); p.Status != "PARTIALLY_COMPLETED" || p.Returned != 1 {
		t.Fatalf("returned: %+v", p)
	}
	if _, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionRetryFailed, w(ngozi, "r4")); payrolla.Code(err) != "INVALID_STATE" {
		t.Fatalf("returned is not retried: %v", err)
	}
}

// Pay now with a code pays a locked run at once.
func TestPayNow(t *testing.T) {
	f, cl := runWorld(t)
	ctx := context.Background()
	ngozi, musa := uuid.New(), uuid.New()
	link(t, f, cl, ngoziID, ngozi)
	link(t, f, cl, musaID, musa)
	run := lockedRun(t, f, cl, ngozi, musa)
	f.Runs().WalletKobo = 500000000
	w := func(key string) payrolla.Write { return payrolla.Write{Key: key, User: ngozi, Name: "Ngozi"} }
	ch, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionPayNow, w("p1"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := cl.Decide(ctx, run, payrolla.ActionPayNow, ch.ID, f.Code(ngoziID), w("p2"))
	if err != nil || d.Payment.Status != "COMPLETED" || d.Run.Status != "COMPLETED" {
		t.Fatalf("paid now: %+v %v", d, err)
	}
	for _, wr := range f.Writes() {
		if wr.Key == "" || !strings.HasPrefix(wr.ActingUser, "tsk_user_") {
			t.Fatalf("a write without its headers: %+v", wr)
		}
	}
}

// Payment events read as Taskiem's subscription hears them.
func TestReadPaymentEvent(t *testing.T) {
	e, ok := payrolla.ReadPaymentEvent([]byte(`{"id":"evt_1","type":"run.awaiting_funds","data":{"run":{"run_id":"r1"},"needed_kobo":10,"available_kobo":4,"short_by_kobo":6}}`))
	if !ok || e.RunID != "r1" || e.ShortKobo != 6 {
		t.Fatalf("%+v", e)
	}
	e, ok = payrolla.ReadPaymentEvent([]byte(`{"id":"evt_2","type":"payslip.issued","data":{"payslip_id":"p","employee_id":"e","run_id":"r2","period":"November 2026"}}`))
	if !ok || e.Payslip != "p" || e.Employee != "e" || e.RunID != "r2" || e.Period != "November 2026" {
		t.Fatalf("%+v", e)
	}
	if _, ok := payrolla.ReadPaymentEvent([]byte(`{"type":"run.computed","data":{"run":{"run_id":"r"}}}`)); ok {
		t.Fatal("run.computed is not a payment's")
	}
}
