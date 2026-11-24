package runtime_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/history"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A signal step that timed out no longer waits. A signal that arrives
// after it is buffered (spec 4.6: never dropped), and a later step that
// waits for the same event and correlation receives it.
func TestTimedOutSignalStepDoesNotSwallowALaterSignal(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"first","type":"signal","config":{"event":"fakepay@1:transfer","correlation":"=trigger.ref","timeout":"1h"},
	   "on_error":{"steps":[
	     {"id":"ok","type":"approval","config":{"role":"ops"}},
	     {"id":"again","type":"signal","needs":["ok"],"config":{"event":"fakepay@1:transfer","correlation":"=trigger.ref","timeout":"1h"}}]}}`, ""))
	ref := e.Start(t, wf, map[string]any{"ref": "R9"})
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE timers SET fire_at = now() - interval '1 second' WHERE run_id = $1`, ref.ID); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h := events(t, e, ref)
	if count(h, history.StepFailed, "first") != 1 || count(h, history.ApprovalRequested, "ok") != 1 {
		t.Fatalf("the first wait should have timed out: %s", types(h))
	}
	if n := waits(t, e, ref.ID.String()); n != 0 {
		t.Errorf("%d waits left by a step that timed out", n)
	}

	// Nobody waits now: the signal is kept for whoever waits next.
	woke, err := e.Store.DeliverSignal(ctx, e.Tenant, "prod", "fakepay@1:transfer", "R9", map[string]any{"status": "success"})
	if err != nil || len(woke) != 0 {
		t.Fatalf("deliver: woke %v: %v", woke, err)
	}
	if err := e.Store.DecideApproval(ctx, ref, "ok", "approved", "checker", "web"); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h = events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" || count(h, history.StepCompleted, "again") != 1 {
		t.Errorf("the later wait did not receive the signal: status %s: %s", st, types(h))
	}
}

// A wait row left by an older engine for a step that has since timed out
// is passed over by a delivery, which buffers the signal instead.
func TestDeliveryPassesOverAWaitWhoseStepIsSettled(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"first","type":"signal","config":{"event":"fakepay@1:transfer","correlation":"=trigger.ref","timeout":"1h"},
	   "on_error":{"steps":[
	     {"id":"ok","type":"approval","config":{"role":"ops"}},
	     {"id":"again","type":"signal","needs":["ok"],"config":{"event":"fakepay@1:transfer","correlation":"=trigger.ref","timeout":"1h"}}]}}`, ""))
	ref := e.Start(t, wf, map[string]any{"ref": "R8"})
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE timers SET fire_at = now() - interval '1 second' WHERE run_id = $1`, ref.ID); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	// As the older engine left it.
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO signal_waits (tenant_id, run_id, step_id, environment, event, correlation) VALUES ($1, $2, 'first', 'prod', 'fakepay@1:transfer', 'R8')`,
		e.Tenant, ref.ID); err != nil {
		t.Fatal(err)
	}
	woke, err := e.Store.DeliverSignal(ctx, e.Tenant, "prod", "fakepay@1:transfer", "R8", map[string]any{"status": "success"})
	if err != nil || len(woke) != 0 {
		t.Fatalf("deliver: woke %v: %v", woke, err)
	}
	if n := waits(t, e, ref.ID.String()); n != 0 {
		t.Errorf("the stale wait is still there (%d)", n)
	}
	if err := e.Store.DecideApproval(ctx, ref, "ok", "approved", "checker", "web"); err != nil {
		t.Fatal(err)
	}
	e.Drain(t)
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" || count(h, history.StepCompleted, "again") != 1 || count(h, history.SignalReceived, "first") != 0 {
		t.Errorf("status %s: %s", st, types(h))
	}
}
