package runtime_test

// A step that will start nothing more (its scope is failing, or another
// step failed the run) is given up or settled, never waited for without
// end, and a write that may have been applied is never reported failed.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// holdTask makes a step's task look claimed by a slow worker, so the
// others are decided first; releaseTask gives it back.
func holdTask(t *testing.T, e *rt.Env, ref runtime.RunRef, step string) {
	t.Helper()
	tag, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_owner = 'w-slow', lease_until = now() + interval '1 minute' WHERE run_id = $1 AND step_id = $2`, ref.ID, step)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("hold %s: %v (%d rows)", step, err, tag.RowsAffected())
	}
}

func releaseTask(t *testing.T, e *rt.Env, ref runtime.RunRef, step string) {
	t.Helper()
	tag, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_owner = NULL, lease_until = NULL WHERE run_id = $1 AND step_id = $2`, ref.ID, step)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("release %s: %v (%d rows)", step, err, tag.RowsAffected())
	}
}

// settleRun drains repeatedly for well past the 10ms backoffs.
func settleRun(t *testing.T, e *rt.Env) {
	t.Helper()
	for i := 0; i < 15; i++ {
		e.Drain(t)
		time.Sleep(40 * time.Millisecond)
	}
	e.Drain(t)
}

func pendingWork(t *testing.T, e *rt.Env, ref runtime.RunRef) (tasks, timers int) {
	t.Helper()
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE run_id = $1`, ref.ID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM timers WHERE run_id = $1 AND fired_at IS NULL`, ref.ID).Scan(&timers); err != nil {
		t.Fatal(err)
	}
	return tasks, timers
}

// mustNotHang fails the test if the run is neither ended nor has anything
// left that could ever move it.
func mustNotHang(t *testing.T, e *rt.Env, ref runtime.RunRef) {
	t.Helper()
	st := e.Status(t, ref)
	tasks, timers := pendingWork(t, e, ref)
	h := events(t, e, ref)
	t.Logf("status=%s tasks=%d unfired_timers=%d history: %s", st, tasks, timers, types(h))
	switch st {
	case "failed", "completed", "needs_reconciliation", "cancelled":
		return
	}
	if tasks == 0 && timers == 0 {
		t.Errorf("run is %q with no task and no timer; nothing will ever move it", st)
	}
}

// Parallel: branch a fails for good; then branch b's step fails
// retryably. Nothing schedules that retry, so the step is given up and the
// parallel fails.
func TestFailingParallelDoesNotWaitForARetry(t *testing.T) {
	e := rt.New(t)
	e.Provider.Faults = func(a string) rt.Fault {
		if a == "transfer" {
			return rt.FailBefore // 503; pay_a has retry max 0, so this is final
		}
		return rt.RefuseBefore // notify: connection refused, not sent, retryable
	}
	wf := e.Publish(t, wfDoc(`{"id":"both","type":"parallel","config":{"branches":[
	  {"name":"a","steps":[{"id":"pay_a","type":"connector","connector":"fakepay@1","action":"transfer","retry":{"max":0},
	     "input":{"amount":1,"logical_id":"=run.id + ':a'"}}]},
	  {"name":"b","steps":[{"id":"note_b","type":"connector","connector":"fakepay@1","action":"notify",
	     "retry":{"max":3,"initial":"10ms","backoff":"fixed"},"input":{"logical_id":"=run.id + ':b'"}}]}]}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	holdTask(t, e, ref, "note_b")
	e.Drain(t) // pay_a fails for good; the parallel waits for note_b
	if h := events(t, e, ref); count(h, history.StepFailed, "pay_a") == 0 || e.Status(t, ref) != "running" {
		t.Fatalf("setup: pay_a should have failed with the run still running: %s %s", e.Status(t, ref), types(h))
	}
	releaseTask(t, e, ref, "note_b")
	settleRun(t, e)
	if h := events(t, e, ref); count(h, history.StepFailed, "note_b") == 0 {
		t.Fatalf("setup: note_b never failed: %s", types(h))
	}
	mustNotHang(t, e, ref)
}

// Foreach: item 0 exhausts its retries; then item 1's first
// attempt fails retryably.
func TestFailingForeachDoesNotWaitForARetry(t *testing.T) {
	e := rt.New(t)
	e.Provider.Faults = func(string) rt.Fault { return rt.RefuseBefore }
	wf := e.Publish(t, wfDoc(`{"id":"each","type":"foreach","config":{"items":"=[0,1]","steps":[
	  {"id":"note","type":"connector","connector":"fakepay@1","action":"notify",
	   "retry":{"max":1,"initial":"10ms","backoff":"fixed"},"input":{"logical_id":"=run.id + ':' + string(item)"}}]}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	holdTask(t, e, ref, "each[1].note")
	settleRun(t, e) // item 0: attempt 1 fails, retry, attempt 2 fails: gave up
	if h := events(t, e, ref); count(h, history.StepFailed, "each[0].note") < 2 || e.Status(t, ref) != "running" {
		t.Fatalf("setup: item 0 should have given up with the run still running: %s %s", e.Status(t, ref), types(h))
	}
	releaseTask(t, e, ref, "each[1].note")
	settleRun(t, e)
	if h := events(t, e, ref); count(h, history.StepFailed, "each[1].note") == 0 {
		t.Fatalf("setup: item 1 never failed: %s", types(h))
	}
	mustNotHang(t, e, ref)
}

// Attempt 1 of an idempotent write is applied at the provider
// but the answer is lost (unknown outcome, may have applied); attempt 2 is
// refused with a fatal error (a 4xx). The run must be parked for an
// operator, not reported failed: the money moved.
func TestMaybeAppliedThenFatalIsParked(t *testing.T) {
	e := rt.New(t)
	conn := e.Provider.Connector()
	conn.Manifest.ID = "flakypay"
	inner := conn.Actions["transfer"]
	var calls atomic.Int32
	conn.Actions["transfer"] = connector.ActionFunc(func(c context.Context, r connector.Request) (connector.Response, error) {
		if calls.Add(1) == 1 {
			return inner.Execute(c, r) // FailAfter: recorded, then the connection drops
		}
		return connector.Response{}, fmt.Errorf("401 unauthorized: %w", effects.ErrFatal)
	})
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	e.Provider.Faults = func(string) rt.Fault { return rt.FailAfter }
	wf := e.Publish(t, wfDoc(`{"id":"pay","type":"connector","connector":"flakypay@1","action":"transfer",
	  "input":{"amount":1,"logical_id":"=run.id + ':pay'"},"retry":{"max":5,"initial":"10ms","backoff":"fixed"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	rt.WaitFor(t, 10*time.Second, "the second attempt", func() bool { e.Drain(t); return calls.Load() >= 2 && e.Status(t, ref) != "running" })
	h := events(t, e, ref)
	st, n := e.Status(t, ref), e.Provider.Executions(ref.ID.String()+":pay")
	t.Logf("status=%s provider executions=%d calls=%d history: %s", st, n, calls.Load(), types(h))
	for _, ev := range h {
		if ev.Type == history.StepFailed || ev.Type == history.RunFailed {
			t.Logf("  %s(%s) attempt %d: %s", ev.Type, ev.StepID, ev.Attempt, ev.Payload)
		}
	}
	if n != 1 {
		t.Fatalf("setup: the provider should have recorded the transfer once, got %d", n)
	}
	if st != "needs_reconciliation" {
		t.Errorf("run is %q, want needs_reconciliation: the provider recorded the transfer once", st)
	}
}

// At top level, another step fails the run while a reconcilable
// write's outcome is unknown (applied at the provider, reconcile not yet
// scheduled). The run must not end failed with the write unaccounted for.
func TestFailingRunReconcilesBeforeItEnds(t *testing.T) {
	e := rt.New(t)
	e.Provider.Faults = func(a string) rt.Fault {
		if a == "notify" {
			return rt.RefuseBefore // retry max 0: final
		}
		if a == "transfer" {
			return rt.FailAfter // applied, answer lost
		}
		return rt.NoFault
	}
	wf := e.Publish(t, wfDoc(`{"id":"note","type":"connector","connector":"fakepay@1","action":"notify","retry":{"max":0},"input":{"logical_id":"=run.id + ':note'"}},
	  {"id":"pay","type":"connector","connector":"fakepay@1","action":"bank_transfer",
	   "input":{"amount":1,"logical_id":"=run.id + ':pay'"},"retry":{"max":3,"initial":"10ms","backoff":"fixed"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	holdTask(t, e, ref, "pay")
	e.Drain(t) // note fails for good; the run waits for pay, which is in flight
	if h := events(t, e, ref); count(h, history.StepFailed, "note") == 0 || e.Status(t, ref) != "running" {
		t.Fatalf("setup: note should have failed with the run still running: %s %s", e.Status(t, ref), types(h))
	}
	releaseTask(t, e, ref, "pay")
	settleRun(t, e)
	h := events(t, e, ref)
	st, n := e.Status(t, ref), e.Provider.Executions(ref.ID.String()+":pay")
	t.Logf("status=%s provider executions=%d history: %s", st, n, types(h))
	for _, ev := range h {
		if ev.Type == history.StepFailed || ev.Type == history.RunFailed {
			t.Logf("  %s(%s) attempt %d: %s", ev.Type, ev.StepID, ev.Attempt, ev.Payload)
		}
	}
	if n != 1 {
		t.Fatalf("setup: the provider should have recorded the transfer once, got %d", n)
	}
	if st == "failed" && count(h, history.StepCompleted, "pay") == 0 {
		t.Errorf("run is failed, pay never reconciled or parked, yet the provider recorded the transfer once")
	}
}
