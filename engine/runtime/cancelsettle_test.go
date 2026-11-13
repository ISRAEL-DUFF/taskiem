package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
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

// lastFailure is the error of the step's latest StepFailed.
func lastFailure(h []history.Event, step string) history.Error {
	var p history.FailedPayload
	for _, ev := range h {
		if ev.Type == history.StepFailed && ev.StepID == step {
			p = history.FailedPayload{}
			_ = json.Unmarshal(ev.Payload, &p)
		}
	}
	return p.Error
}

// slowRetry is a payment whose retry waits an hour: a cancel always finds
// it between attempts.
func slowRetry(action string) string {
	return `{"id":"pay","type":"connector","connector":"fakepay@1","action":"` + action + `",
	  "input":{"amount":1,"logical_id":"=run.id + ':pay'"},"retry":{"max":3,"initial":"1h","backoff":"fixed"}}`
}

// A run is cancelled, with reversing, while an idempotent write waits for
// its retry after an attempt whose answer was lost. The write is not
// forgotten: it is sent again under the same key to find out, and only
// then is the run cancelled.
func TestCancelDuringBackoffSettlesAnUnknownOutcome(t *testing.T) {
	e := rt.New(t)
	var calls atomic.Int32
	e.Provider.Faults = func(string) rt.Fault {
		if calls.Add(1) == 1 {
			return rt.FailAfter // applied, answer lost
		}
		return rt.NoFault
	}
	wf := e.Publish(t, wfDoc(slowRetry("transfer"), ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if h := events(t, e, ref); count(h, history.StepScheduled, "pay") != 2 || tasksLeft(t, e, ref) != 1 {
		dump(t, e, ref)
		t.Fatalf("setup: pay should be waiting for its retry")
	}
	if err := e.Store.CancelRun(ctx, ref, "tester", true); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "cancelled" || count(h, history.StepCompleted, "pay") != 1 {
		dump(t, e, ref)
		t.Fatalf("run is %s; pay should have been settled as completed, then the run cancelled", st)
	}
	if n := e.Provider.Executions(ref.ID.String() + ":pay"); n != 1 || count(h, history.StepScheduled, "pay") != 3 {
		t.Errorf("provider executions %d, want 1 (the same key again); attempts %d, want 3", n, count(h, history.StepScheduled, "pay"))
	}
}

// The same for a reconcilable write the provider never received: the
// reconcile finds nothing, and the step fails without being sent again.
func TestCancelDuringBackoffReconcilesAndDoesNotSendAgain(t *testing.T) {
	e := rt.New(t)
	e.CallTimeout = 200 * time.Millisecond
	conn := e.Provider.Connector()
	conn.Manifest.ID = "lostpay"
	var calls atomic.Int32
	conn.Actions["bank_transfer"] = connector.ActionFunc(func(context.Context, connector.Request) (connector.Response, error) {
		calls.Add(1)
		return connector.Response{}, fmt.Errorf("connection reset: %w", effects.ErrUnknownOutcome) // never arrived
	})
	if err := e.Registry.Register(conn); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, wfDoc(`{"id":"pay","type":"connector","connector":"lostpay@1","action":"bank_transfer",
	  "input":{"amount":1,"logical_id":"=run.id + ':pay'"},"retry":{"max":3,"initial":"1h","backoff":"fixed"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if err := e.Store.CancelRun(ctx, ref, "tester", true); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(t, ref); st == "cancelled" {
		dump(t, e, ref)
		t.Fatal("cancelled with pay's outcome unknown")
	}
	rt.WaitFor(t, 10*time.Second, "the reconcile", func() bool { e.Drain(t); return e.Status(t, ref) == "cancelled" })
	h := events(t, e, ref)
	if f := lastFailure(h, "pay"); f.Kind != history.KindNotApplied || f.Next != "fail" {
		dump(t, e, ref)
		t.Errorf("pay ended %+v, want not_applied", f)
	}
	if calls.Load() != 1 || count(h, history.EffectIntent, "pay") != 1 {
		t.Errorf("pay was sent again after the cancel: %d calls, %d intents", calls.Load(), count(h, history.EffectIntent, "pay"))
	}
}

// A worker dies after sending a reconcilable write; the run is cancelled
// with reversing. The re-claimed task reconciles, finds the transfer, and
// the run is cancelled, where it used to be picked up at every lease for
// ever.
func TestCancelReconcilesAStepWithAnOpenIntent(t *testing.T) {
	e := rt.New(t)
	e.CallTimeout = 200 * time.Millisecond
	wf := e.Publish(t, wfDoc(slowRetry("bank_transfer"), ""))
	ref := e.Start(t, wf, map[string]any{})
	w := e.Worker("crashy")
	w.Lease = 300 * time.Millisecond
	w.Hooks = &runtime.Hooks{AfterCall: func(string, int) error { return errors.New("kill -9") }}
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.CancelRun(ctx, ref, "tester", true); err != nil {
		t.Fatal(err)
	}
	rt.WaitFor(t, 10*time.Second, "the reconcile", func() bool { e.Drain(t); return e.Status(t, ref) == "cancelled" })
	h := events(t, e, ref)
	if count(h, history.StepCompleted, "pay") != 1 || e.Provider.Executions(ref.ID.String()+":pay") != 1 || tasksLeft(t, e, ref) != 0 {
		dump(t, e, ref)
		t.Errorf("pay should be completed by reconciling, once, with no task left")
	}
}

// A worker dies after recording a write's intent, and the run is then
// cancelled without reversing, which ends it at once. The write is not
// sent again, since nothing would reverse it; its unknown outcome is
// recorded as parked rather than left as an intent with no end.
func TestEndedRunDoesNotSendAgainButRecordsTheUnknownOutcome(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(payOne, ""))
	ref := e.Start(t, wf, map[string]any{"amount": 1})
	w := e.Worker("crashy")
	w.Lease = 300 * time.Millisecond
	w.Hooks = &runtime.Hooks{AfterIntent: func(string, int) error { return errors.New("kill -9") }}
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.CancelRun(ctx, ref, "tester", false); err != nil {
		t.Fatal(err)
	}
	if tasksLeft(t, e, ref) != 1 {
		t.Fatal("setup: the task with an open intent should be kept")
	}
	time.Sleep(400 * time.Millisecond) // the lease expires
	e.Drain(t)
	h := events(t, e, ref)
	if n := e.Provider.Executions(ref.ID.String() + ":pay"); n != 0 || count(h, history.EffectIntent, "pay") != 1 {
		dump(t, e, ref)
		t.Errorf("pay was sent after the run ended: %d executions, %d intents", n, count(h, history.EffectIntent, "pay"))
	}
	if f := lastFailure(h, "pay"); f.Next != "park" || !f.MaybeApplied {
		dump(t, e, ref)
		t.Errorf("pay's unknown outcome was not recorded: %+v", f)
	}
	if st := e.Status(t, ref); st != "cancelled" || tasksLeft(t, e, ref) != 0 {
		t.Errorf("run is %s with %d tasks left, want cancelled with none", st, tasksLeft(t, e, ref))
	}
}

// A run an older engine left waiting: its cancelled step has an open
// intent and the task was dropped, so no outcome would ever come. Asking
// to cancel again gives the write its task back, and the run is cancelled.
func TestCancelAgainMovesOnARunLeftWithAnOpenIntent(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(payOne, ""))
	ref := e.Start(t, wf, map[string]any{"amount": 1})
	w := e.Worker("crashy")
	w.Hooks = &runtime.Hooks{AfterCall: func(string, int) error { return errors.New("kill -9") }}
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.CancelRun(ctx, ref, "tester", true); err != nil {
		t.Fatal(err)
	}
	// What the older engine did when the task was claimed again.
	if _, err := e.DB.Admin.Exec(ctx, `DELETE FROM tasks WHERE run_id = $1`, ref.ID); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	if st := e.Status(t, ref); st == "cancelled" {
		t.Fatalf("setup: the run should be stuck, it is %s", st)
	}
	if err := e.Store.CancelRun(ctx, ref, "tester", true); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "cancelled" || count(h, history.StepCompleted, "pay") != 1 || e.Provider.Executions(ref.ID.String()+":pay") != 1 {
		dump(t, e, ref)
		t.Errorf("run is %s, want cancelled with pay settled by the same key", st)
	}
	if count(h, history.CancelRequested, "") != 1 {
		t.Errorf("the second request was recorded: %s", types(h))
	}
}

// A reversing cancel waits for parked steps. Resolving them one by one
// must work, and the run is cancelled once the last is resolved.
func TestCancelWaitsForEachParkedStepToBeResolved(t *testing.T) {
	e := rt.New(t)
	e.Provider.Faults = func(string) rt.Fault { return rt.FailAfter }
	wf := e.Publish(t, wfDoc(`{"id":"sms_a","type":"connector","connector":"fakepay@1","action":"notify","input":{"logical_id":"=run.id + ':a'"}},
	  {"id":"sms_b","type":"connector","connector":"fakepay@1","action":"notify","input":{"logical_id":"=run.id + ':b'"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if err := e.Store.CancelRun(ctx, ref, "tester", true); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"sms_a", "sms_b"} {
		if st := e.Status(t, ref); st != "needs_reconciliation" {
			dump(t, e, ref)
			t.Fatalf("before resolving %s the run is %s, want needs_reconciliation", step, st)
		}
		if err := e.Store.ResolveStep(ctx, ref, step, runtime.ResolveFailed, nil, "not at the provider", "ops"); err != nil {
			t.Fatalf("resolve %s: %v", step, err)
		}
		e.Drain(t)
	}
	if st := e.Status(t, ref); st != "cancelled" {
		dump(t, e, ref)
		t.Errorf("run is %s, want cancelled", st)
	}
}
