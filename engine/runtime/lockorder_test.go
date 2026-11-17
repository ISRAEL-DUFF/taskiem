package runtime_test

import (
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// lockWaiters is how many of the test database's sessions wait for a lock.
func lockWaiters(t *testing.T, e *rt.Env) int {
	t.Helper()
	var n int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A run's last step is recorded while its run timeout comes due. The
// worker holds the run and removes its timers; the scheduler takes the run
// before the timer too, so it waits its turn and finds the run ended.
// Taking the timer first, the two waited on each other and the database
// ended one of them.
func TestTimerFiringWaitsForTheRunItBelongsTo(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(payOne, `{"timeout":"1h"}`))
	ref := e.Start(t, wf, map[string]any{"amount": 5})
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE timers SET fire_at = now() - interval '1 second' WHERE run_id = $1`, ref.ID); err != nil {
		t.Fatal(err)
	}

	called, proceed := make(chan struct{}), make(chan struct{})
	w := e.Worker("w-last")
	w.Hooks = &runtime.Hooks{AfterCall: func(string, int) error { close(called); <-proceed; return nil }}
	workerErr, tickErr := make(chan error, 1), make(chan error, 1)
	go func() { _, err := w.RunOnce(ctx); workerErr <- err }()
	<-called

	// The run is held, so each side stops where it asks for it: the worker
	// first, then the scheduler.
	hold, err := e.DB.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM runs WHERE id = $1 FOR UPDATE`, ref.ID); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	rt.WaitFor(t, 10*time.Second, "the worker to wait for the run", func() bool { return lockWaiters(t, e) == 1 })
	go func() { _, err := e.Scheduler().Tick(ctx); tickErr <- err }()
	rt.WaitFor(t, 10*time.Second, "the scheduler to wait for the run", func() bool { return lockWaiters(t, e) == 2 })
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if err := <-workerErr; err != nil {
		t.Errorf("the worker: %v", err)
	}
	if err := <-tickErr; err != nil {
		t.Errorf("the scheduler: %v", err)
	}
	h := events(t, e, ref)
	if st := e.Status(t, ref); st != "completed" || count(h, history.StepCompleted, "pay") != 1 || count(h, history.TimerFired, "") != 0 {
		t.Errorf("status %s: %s", st, types(h))
	}
}

// A child run's last step is recorded while its parent is cancelled. Each
// holds its own run and asks for the other's (the child to report, the
// parent to cancel the child), so the database ends one of them. It is run
// again: the step's outcome is not lost, and the cancel is not refused.
func TestParentAndChildEndingTogetherLoseNothing(t *testing.T) {
	e := rt.New(t)
	child := e.Publish(t, `{"schema":"wd/v1","id":"wf_c","version":1,"name":"Child","trigger":{"type":"subflow"},"steps":[
	  {"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer","input":{"amount":"=trigger.body.amount","logical_id":"=run.id + ':pay'"}}]}`)
	parent := e.Publish(t, wfDoc(`{"id":"sub","type":"subflow","config":{"workflow":"`+child.String()+`"},"input":{"amount":7}}`, ""))
	ref := e.Start(t, parent, map[string]any{})
	links, err := e.Store.Forks(ctx, ref)
	if err != nil || len(links.Subflows) != 1 {
		t.Fatalf("the child run: %+v %v", links, err)
	}
	childRef := runtime.RunRef{ID: links.Subflows[0], TenantID: e.Tenant}

	called, proceed := make(chan struct{}), make(chan struct{})
	w := e.Worker("w-child")
	w.Hooks = &runtime.Hooks{AfterCall: func(string, int) error { close(called); <-proceed; return nil }}
	workerErr, cancelErr := make(chan error, 1), make(chan error, 1)
	go func() { _, err := w.RunOnce(ctx); workerErr <- err }()
	<-called

	// The step's wait for the child is held, so both stop at it: the
	// cancel holding the parent, then the worker holding the child.
	hold, err := e.DB.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM signal_waits WHERE run_id = $1 FOR UPDATE`, ref.ID); err != nil {
		t.Fatal(err)
	}
	go func() { cancelErr <- e.Store.CancelRun(ctx, ref, "someone", false) }()
	rt.WaitFor(t, 10*time.Second, "the cancel to wait", func() bool { return lockWaiters(t, e) == 1 })
	close(proceed)
	rt.WaitFor(t, 10*time.Second, "the worker to wait", func() bool { return lockWaiters(t, e) == 2 })
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-workerErr; err != nil {
		t.Errorf("the worker lost its outcome: %v", err)
	}
	if err := <-cancelErr; err != nil {
		t.Errorf("the cancel: %v", err)
	}
	ph, ch := events(t, e, ref), events(t, e, childRef)
	// Whichever went first: the parent was cancelled, or heard from the
	// child before the cancel got to it.
	if st := e.Status(t, ref); st != "cancelled" && (st != "completed" || count(ph, history.StepCompleted, "sub") != 1) {
		t.Errorf("parent %s: %s", st, types(ph))
	}
	if st := e.Status(t, childRef); (st != "completed" && st != "cancelled") || count(ch, history.StepCompleted, "pay") != 1 ||
		e.Provider.Executions(childRef.ID.String()+":pay") != 1 {
		t.Errorf("child %s: %s", st, types(ch))
	}
}
