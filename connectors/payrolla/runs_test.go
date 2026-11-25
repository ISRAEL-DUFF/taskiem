package payrolla_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/payrolla"
	"github.com/israel-duff/taskiem/connectors/payrolla/payrollatest"
)

// The run routes the fake serves are Payrolla's, with its error codes.
func TestFakeRunsFollowTheOpenAPIFile(t *testing.T) {
	raw, err := os.ReadFile("../../docs/integrations/payrolla-reference/api/payrolla-connect-v1-2026-11-24.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	for _, r := range payrollatest.RunRoutes() {
		method, path, _ := strings.Cut(r, " ")
		block := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(path) + `:\n((?:    .*\n|\n)*)`).FindStringSubmatch(spec)
		if block == nil || !strings.Contains(block[1], "    "+strings.ToLower(method)+":") {
			t.Errorf("the fake serves %s, which Payrolla's OpenAPI file does not have", r)
		}
	}
	for _, c := range payrollatest.RunErrorCodes() {
		if !strings.Contains(spec, c) {
			t.Errorf("the fake answers %s, which Payrolla's OpenAPI file does not name", c)
		}
	}
}

const (
	ngoziID = "a0000000-0000-4000-8000-000000000001"
	musaID  = "a0000000-0000-4000-8000-000000000002"
)

func runWorld(t *testing.T) (*payrollatest.Fake, *payrolla.Client) {
	t.Helper()
	f := payrollatest.New(taskiemApp)
	t.Cleanup(f.Close)
	f.ServeRuns()
	f.SetWorkers(payrollatest.Worker{ID: "e0000000-0000-4000-8000-000000000001", First: "Zainab", Last: "Bello", GrossKobo: 45000000},
		payrollatest.Worker{ID: "e0000000-0000-4000-8000-000000000002", First: "Musa", Last: "Ali", GrossKobo: 30000000},
		payrollatest.Worker{ID: "e0000000-0000-4000-8000-000000000003", First: "Ada", Last: "Obi", GrossKobo: 25000000})
	f.AddApprover(payrollatest.Approver{UserID: ngoziID, Name: "Ngozi Eze", Role: "OWNER", CanApprove: true, CanPay: true, CanConfirm: true})
	f.AddApprover(payrollatest.Approver{UserID: musaID, Name: "Musa Bello", Role: "APPROVER", CanApprove: true, CanConfirm: true})
	st := &store{creds: connectOAuth(t, f)}
	return f, payrolla.NewClient(newConnector(f), st.get(), http.DefaultClient, st.rotate)
}

func link(t *testing.T, f *payrollatest.Fake, cl *payrolla.Client, user string, person uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	w := payrolla.Write{Key: "link-" + person.String(), User: person, Name: "x"}
	ch, err := cl.StartLink(ctx, user, w)
	if err != nil {
		t.Fatal(err)
	}
	w.Key += "-confirm"
	if _, err := cl.ConfirmLink(ctx, user, ch.ID, f.Code(user), w); err != nil {
		t.Fatal(err)
	}
}

// November's payroll: prepared, a bonus and a deduction, someone left out
// and put back, approved by someone other than whoever prepared it with a
// code, and locked: the payment scheduled for the pay date.
func TestARunFromPreparedToLocked(t *testing.T) {
	f, cl := runWorld(t)
	f.Runs().SeparationOfDuties = true
	ctx := context.Background()
	preparer, approver := uuid.New(), uuid.New()
	link(t, f, cl, ngoziID, preparer)
	link(t, f, cl, musaID, approver)
	w := func(who uuid.UUID, key string) payrolla.Write {
		return payrolla.Write{Key: key, User: who, Name: "Someone"}
	}

	got, err := cl.CreateRun(ctx, payrolla.Period{Period: "2026-11"}, w(preparer, "prep"))
	if err != nil || got.Run.Status != "COMPUTED" || got.Run.PayDate != "2026-11-30" || got.Run.EmployeeCount != 3 {
		t.Fatalf("prepared: %+v %v", got, err)
	}
	run := got.Run.ID
	if _, err := cl.CreateRun(ctx, payrolla.Period{Period: "2026-11"}, w(preparer, "prep-again")); payrolla.Code(err) != "DUPLICATE_RUN" {
		t.Fatalf("a second: %v", err)
	}
	lines, err := cl.Lines(ctx, run)
	if err != nil || len(lines) != 3 || len(lines[0].Items) < 5 || lines[0].GrossKobo != 45000000 {
		t.Fatalf("lines: %+v %v", lines, err)
	}
	zainab := lines[0].EmployeeID
	before := lines[0].NetKobo
	ch, err := cl.AddAdjustment(ctx, run, zainab, "BONUS", 10000000, "Q4 target", w(preparer, "bonus"))
	if err != nil || ch.Adjustment == nil || ch.Lines[0].NetKobo <= before {
		t.Fatalf("bonus: %+v %v", ch, err)
	}
	if _, err := cl.AddAdjustment(ctx, run, zainab, "DEDUCTION", 2000000, "Advance repaid", w(preparer, "deduct")); err != nil {
		t.Fatal(err)
	}
	ex, err := cl.Exclude(ctx, run, "e0000000-0000-4000-8000-000000000003", "unpaid leave", w(preparer, "out"))
	if err != nil || ex.Run.EmployeeCount != 2 || ex.Exclusion == nil {
		t.Fatalf("left out: %+v %v", ex, err)
	}
	// Left out twice is taken as done.
	if again, err := cl.Exclude(ctx, run, "e0000000-0000-4000-8000-000000000003", "unpaid leave", w(preparer, "out2")); err != nil || again.Run.EmployeeCount != 2 {
		t.Fatalf("again: %v", err)
	}
	back, err := cl.PutBack(ctx, run, ex.Exclusion.ID, w(preparer, "back"))
	if err != nil || back.Run.EmployeeCount != 3 {
		t.Fatalf("put back: %+v %v", back, err)
	}
	adj, _ := cl.Adjustments(ctx, run)
	if len(adj) != 2 {
		t.Fatalf("adjustments: %v", adj)
	}
	if _, err := cl.RemoveAdjustment(ctx, run, adj[1].ID, w(preparer, "undo")); err != nil {
		t.Fatal(err)
	}

	// The preparer can't approve it; another can, with a code.
	if _, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionApprove, w(preparer, "c1")); payrolla.Code(err) != "SOD_VIOLATION" {
		t.Fatalf("sod: %v", err)
	}
	if _, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionApprove, w(uuid.New(), "c2")); payrolla.Code(err) != "APPROVER_NOT_LINKED" {
		t.Fatalf("not linked: %v", err)
	}
	if _, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionLock, w(approver, "c3")); payrolla.Code(err) != "INVALID_STATE" {
		t.Fatalf("lock first: %v", err)
	}
	code, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionApprove, w(approver, "c4"))
	if err != nil || code.ID == "" {
		t.Fatal(err)
	}
	if _, err := cl.Decide(ctx, run, payrolla.ActionApprove, code.ID, "000000", w(approver, "d1")); payrolla.Code(err) != "CODE_INVALID" {
		t.Fatalf("wrong: %v", err)
	}
	if d, err := cl.Decide(ctx, run, payrolla.ActionApprove, code.ID, f.Code(musaID), w(approver, "d2")); err != nil || d.Run.Status != "APPROVED" {
		t.Fatalf("approved: %+v %v", d, err)
	}
	if _, err := cl.Decide(ctx, run, payrolla.ActionApprove, code.ID, f.Code(musaID), w(approver, "d3")); payrolla.Code(err) != "CODE_USED" {
		t.Fatalf("used: %v", err)
	}
	if _, err := cl.AddAdjustment(ctx, run, zainab, "BONUS", 1, "late", w(preparer, "late")); payrolla.Code(err) != "INVALID_STATE" {
		t.Fatalf("after approval: %v", err)
	}

	// Locking is refused while someone has no account, naming them.
	ws := f.Workers()
	ws[1].NoAccount = true
	f.SetWorker(ws[1])
	_, err = cl.StartRunConfirmation(ctx, run, payrolla.ActionLock, w(approver, "l1"))
	var det struct {
		IDs []string `json:"employee_ids"`
	}
	_ = json.Unmarshal(payrolla.ErrorDetails(err), &det)
	if payrolla.Code(err) != "NO_PAYOUT_DESTINATION" || len(det.IDs) != 1 {
		t.Fatalf("no account: %v %v", err, det)
	}
	ws[1].NoAccount = false
	f.SetWorker(ws[1])
	lock, err := cl.StartRunConfirmation(ctx, run, payrolla.ActionLock, w(approver, "l2"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	f.Now = func() time.Time { return now.Add(6 * time.Minute) }
	if _, err := cl.Decide(ctx, run, payrolla.ActionLock, lock.ID, f.Code(musaID), w(approver, "l3")); payrolla.Code(err) != "CODE_EXPIRED" {
		t.Fatalf("expired: %v", err)
	}
	lock, _ = cl.StartRunConfirmation(ctx, run, payrolla.ActionLock, w(approver, "l4"))
	d, err := cl.Decide(ctx, run, payrolla.ActionLock, lock.ID, f.Code(musaID), w(approver, "l5"))
	if err != nil || d.Run.Status != "LOCKED" || d.Payment.Status != "SCHEDULED" || d.Payment.ScheduledFor != "2026-11-30" {
		t.Fatalf("locked: %+v %v", d, err)
	}
	q := f.Queued()
	if n := len(q); n < 3 || q[n-3] != "run.locked" || q[n-2] != "approval.recorded" || q[n-1] != "run.payment_scheduled" {
		t.Fatalf("events: %v", q)
	}
	for _, wr := range f.Writes() {
		if wr.Key == "" || !strings.HasPrefix(wr.ActingUser, "tsk_user_") {
			t.Fatalf("a write without its headers: %+v", wr)
		}
	}
}

// A run Payrolla can't work out is made all the same, with why; rejecting
// needs a note and no code; cancelling only before approval; the wallet.
func TestRunComputeErrorRejectCancelFunding(t *testing.T) {
	f, cl := runWorld(t)
	ctx := context.Background()
	me := uuid.New()
	link(t, f, cl, ngoziID, me)
	w := func(key string) payrolla.Write { return payrolla.Write{Key: key, User: me, Name: "Ngozi"} }
	f.Runs().ComputeError = "Pension rates are not set"
	got, err := cl.CreateRun(ctx, payrolla.Period{Period: "2026-12"}, w("p"))
	if err != nil || got.Run.Status != "DRAFT" || got.ComputeError == nil || got.ComputeError.Message != "Pension rates are not set" {
		t.Fatalf("draft: %+v %v", got, err)
	}
	f.Runs().ComputeError = ""
	r, err := cl.Compute(ctx, got.Run.ID, w("c"))
	if err != nil || r.Status != "COMPUTED" {
		t.Fatalf("recalculated: %+v %v", r, err)
	}
	if _, err := cl.Reject(ctx, r.ID, "", w("r0")); err == nil {
		t.Fatal("a rejection with no note")
	}
	d, err := cl.Reject(ctx, r.ID, "Musa's bonus is wrong", w("r1"))
	if err != nil || d.Run.Status != "DRAFT" {
		t.Fatalf("rejected: %+v %v", d, err)
	}
	if c, err := cl.Cancel(ctx, r.ID, w("x")); err != nil || c.Status != "CANCELLED" {
		t.Fatalf("cancelled: %+v %v", c, err)
	}
	if _, err := cl.Cancel(ctx, r.ID, w("x2")); payrolla.Code(err) != "INVALID_STATE" {
		t.Fatalf("cancelled twice: %v", err)
	}
	f.Runs().WalletKobo = 5000000
	fu, err := cl.Funding(ctx)
	if err != nil || !fu.HasWallet || fu.AvailableKobo != 5000000 {
		t.Fatalf("funding: %+v %v", fu, err)
	}
	// Five codes in ten minutes, then no more.
	f.Runs().ComputeError = ""
	again, _ := cl.CreateRun(ctx, payrolla.Period{Period: "2027-01"}, payrolla.Write{Key: "p2", User: uuid.New(), Name: "x"})
	for i := range 5 {
		_, err = cl.StartRunConfirmation(ctx, again.Run.ID, payrolla.ActionApprove, w("k"+string(rune('a'+i))))
	}
	if payrolla.Code(err) != "TOO_MANY_CODES" {
		t.Fatalf("too many: %v", err)
	}
}

// Events about a run read as Taskiem's subscription hears them.
func TestReadRunEvent(t *testing.T) {
	body := `{"id":"evt_1","type":"approval.recorded","data":{"subject":"run","subject_id":"r1","action":"approved","by":{"user_id":"u","name":"Ngozi Eze"},"via":"payrolla"}}`
	e, ok := payrolla.ReadRunEvent([]byte(body))
	if !ok || e.Run.ID != "r1" || e.By != "Ngozi Eze" || e.Via != "payrolla" || e.Action != "approved" {
		t.Fatalf("%+v", e)
	}
	if _, ok := payrolla.ReadRunEvent([]byte(`{"type":"approval.recorded","data":{"subject":"bank_change","subject_id":"b"}}`)); ok {
		t.Fatal("a bank change is not a run's")
	}
	e, ok = payrolla.ReadRunEvent([]byte(`{"type":"run.payment_scheduled","data":{"run":{"run_id":"r2"},"scheduled_for":"2026-11-28"}}`))
	if !ok || e.ScheduledFor != "2026-11-28" {
		t.Fatalf("%+v", e)
	}
}
