package runtime_test

import (
	"errors"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

func dump(t *testing.T, e *rt.Env, ref runtime.RunRef) {
	t.Helper()
	for _, ev := range events(t, e, ref) {
		t.Logf("  #%d %s(%s) attempt=%d origin=%s %s", ev.Seq, ev.Type, ev.StepID, ev.Attempt, ev.Origin, ev.Payload)
	}
}

func tasksLeft(t *testing.T, e *rt.Env, ref runtime.RunRef) int {
	t.Helper()
	var n int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE run_id = $1`, ref.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Two unsafe writes park in the same run; resolving the first must
// leave the second resolvable and the run able to finish.
func TestResolvingOneParkedStepLeavesTheOtherResolvable(t *testing.T) {
	e := rt.New(t)
	e.Provider.Faults = func(string) rt.Fault { return rt.FailAfter }
	wf := e.Publish(t, wfDoc(`{"id":"sms_a","type":"connector","connector":"fakepay@1","action":"notify","input":{"logical_id":"=run.id + ':a'"}},
	  {"id":"sms_b","type":"connector","connector":"fakepay@1","action":"notify","input":{"logical_id":"=run.id + ':b'"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "needs_reconciliation" || count(h, history.StepFailed, "sms_a") != 1 || count(h, history.StepFailed, "sms_b") != 1 {
		dump(t, e, ref)
		t.Fatalf("setup: status %s; both steps should have parked", st)
	}
	if e.Provider.Executions(ref.ID.String()+":a") != 1 || e.Provider.Executions(ref.ID.String()+":b") != 1 {
		t.Fatalf("setup: provider %v", e.Provider.Snapshot())
	}
	if err := e.Store.ResolveStep(ctx, ref, "sms_a", runtime.ResolveCompleted, map[string]any{"sent": true}, "seen at provider", "ops"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	e.Drain(t)
	st1 := e.Status(t, ref)
	t.Logf("status after resolving sms_a: %s; tasks left: %d", st1, tasksLeft(t, e, ref))
	err := e.Store.ResolveStep(ctx, ref, "sms_b", runtime.ResolveCompleted, map[string]any{"sent": true}, "seen at provider", "ops")
	e.Drain(t)
	st2 := e.Status(t, ref)
	t.Logf("second resolve error: %v; status after: %s", err, st2)
	dump(t, e, ref)
	if st1 != "needs_reconciliation" {
		t.Errorf("after resolving one of two parked steps the run is %q, want needs_reconciliation (sms_b is still parked)", st1)
	}
	if err != nil {
		t.Errorf("resolving the second parked step was refused: %v", err)
	}
	if st2 != "completed" {
		t.Errorf("run ended %q, want completed", st2)
	}
}

// A worker dies after recording an idempotent write's intent; the
// run is cancelled with reverse; the re-claimed task must not be dropped
// without an outcome, and the run must reach cancelled.
func TestCancelSettlesAStepWithAnOpenIntent(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(payOne, ""))
	ref := e.Start(t, wf, map[string]any{"amount": 1})
	w := e.Worker("crashy")
	w.Lease = 300 * time.Millisecond
	// AfterCall: the call reached the provider, the outcome was never recorded.
	w.Hooks = &runtime.Hooks{AfterCall: func(string, int) error { return errors.New("kill -9") }}
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	h := events(t, e, ref)
	if count(h, history.EffectIntent, "pay") != 1 || count(h, history.StepCompleted, "pay") != 0 || e.Provider.Executions(ref.ID.String()+":pay") != 1 || tasksLeft(t, e, ref) != 1 {
		dump(t, e, ref)
		t.Fatalf("setup: want an open intent, one execution, one leased task")
	}
	if err := e.Store.CancelRun(ctx, ref, "tester", true); err != nil {
		t.Fatal(err)
	}
	t.Logf("status right after cancel: %s", e.Status(t, ref))
	time.Sleep(500 * time.Millisecond) // the lease expires
	for i := 0; i < 5; i++ {
		e.Drain(t)
		time.Sleep(100 * time.Millisecond)
	}
	st, left := e.Status(t, ref), tasksLeft(t, e, ref)
	var timers int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM timers WHERE run_id = $1 AND fired_at IS NULL`, ref.ID).Scan(&timers)
	t.Logf("final status %s, tasks left %d, open timers %d, provider %v", st, left, timers, e.Provider.Snapshot())
	dump(t, e, ref)
	if st != "cancelled" {
		t.Errorf("run is %q with %d tasks and %d timers left, want cancelled", st, left, timers)
	}
}

const twoPays = `{"id":"pay_a","type":"connector","connector":"fakepay@1","action":"transfer","retry":{"max":0},"input":{"amount":1,"logical_id":"=run.id + ':a'"}},
	  {"id":"pay_b","type":"connector","connector":"fakepay@1","action":"transfer","retry":{"max":0},"input":{"amount":1,"logical_id":"=run.id + ':b'"}}`

// lateStart holds pay_b's task as a slow worker would (claimed, intent not
// yet recorded), ends the run with end, lets the lease lapse, and reports
// whether the payment then started.
func lateStart(t *testing.T, e *rt.Env, ref runtime.RunRef, want string, end func()) {
	t.Helper()
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_owner = 'w-slow', lease_until = now() + interval '1 minute' WHERE run_id = $1 AND step_id = 'pay_b'`, ref.ID); err != nil {
		t.Fatal(err)
	}
	end()
	if st := e.Status(t, ref); st != want {
		dump(t, e, ref)
		t.Fatalf("setup: run is %s, want %s", st, want)
	}
	if n := e.Provider.Executions(ref.ID.String() + ":b"); n != 0 {
		t.Fatalf("setup: pay_b already executed %d times", n)
	}
	t.Logf("run ended %s with %d task(s) kept", want, tasksLeft(t, e, ref))
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_owner = NULL, lease_until = NULL WHERE run_id = $1`, ref.ID); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h := events(t, e, ref)
	endSeq, intentSeq := int64(0), int64(0)
	for _, ev := range h {
		if ev.Type == history.RunCancelled || ev.Type == history.RunFailed {
			endSeq = ev.Seq
		}
		if ev.Type == history.EffectIntent && ev.StepID == "pay_b" {
			intentSeq = ev.Seq
		}
	}
	n := e.Provider.Executions(ref.ID.String() + ":b")
	t.Logf("status %s; run-end seq %d; pay_b intent seq %d; pay_b provider executions %d", e.Status(t, ref), endSeq, intentSeq, n)
	dump(t, e, ref)
	if n != 0 || intentSeq != 0 {
		t.Errorf("pay_b ran after the run ended: provider executions %d, EffectIntent seq %d after run end seq %d (status %s)", n, intentSeq, endSeq, e.Status(t, ref))
	}
}

// Non-reversing cancel while a payment task is held by a worker.
func TestNoPaymentStartsAfterItsRunIsCancelled(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(twoPays, ""))
	ref := e.Start(t, wf, map[string]any{})
	lateStart(t, e, ref, "cancelled", func() {
		if err := e.Store.CancelRun(ctx, ref, "tester", false); err != nil {
			t.Fatal(err)
		}
	})
}

// The sibling step fails while a payment task is held. Decide
// does not end the run while pay_b is outstanding, so this path cannot
// produce "payment after RunFailed"; the test pins that, and logs what
// pay_b then does.
func TestSiblingFailureDoesNotEndRunWhilePaymentHeld(t *testing.T) {
	e := rt.New(t)
	calls := 0
	e.Provider.Faults = func(string) rt.Fault {
		calls++
		if calls == 1 {
			return rt.FailBefore // pay_a: retryable, but retry.max is 0, so it fails for good
		}
		return rt.NoFault
	}
	wf := e.Publish(t, wfDoc(twoPays, ""))
	ref := e.Start(t, wf, map[string]any{})
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_owner = 'w-slow', lease_until = now() + interval '1 minute' WHERE run_id = $1 AND step_id = 'pay_b'`, ref.ID); err != nil {
		t.Fatal(err)
	}
	// Only pay_a is claimable: pay_b is held by w-slow.
	if _, err := e.Worker("w-a").RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "running" || count(h, history.RunFailed, "") != 0 {
		dump(t, e, ref)
		t.Fatalf("run ended (%s) while pay_b was still held", st)
	}
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_owner = NULL, lease_until = NULL WHERE run_id = $1`, ref.ID); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h = events(t, e, ref)
	t.Logf("final status %s; pay_b executions %d; StepCancelled(pay_b)=%d", e.Status(t, ref), e.Provider.Executions(ref.ID.String()+":b"), count(h, history.StepCancelled, "pay_b"))
	dump(t, e, ref)
	last := h[len(h)-1]
	if last.Type != history.RunFailed {
		t.Errorf("history ends with %s, want RunFailed as the last event", last.Type)
	}
}
