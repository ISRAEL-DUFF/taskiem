package runtime_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// process is a store with a pool of its own, as another process has.
func process(t *testing.T, e *rt.Env, inTx bool) *runtime.Store {
	t.Helper()
	return &runtime.Store{Pool: e.DB.AppPool(t, 8), Registry: e.Registry, PII: e.Vault, HooksURL: "https://hooks.test/hooks", WakeInTx: inTx}
}

// notifications counts what a listener of its own hears on taskiem_tasks
// until the returned function is called.
func notifications(t *testing.T, e *rt.Env) func() int {
	t.Helper()
	lctx, cancel := context.WithCancel(context.Background())
	l, err := pgx.Connect(lctx, e.DB.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Exec(lctx, `LISTEN taskiem_tasks`); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		n := 0
		for {
			if _, err := l.WaitForNotification(lctx); err != nil {
				done <- n
				return
			}
			n++
		}
	}()
	return func() int {
		time.Sleep(200 * time.Millisecond)
		cancel()
		n := <-done
		_ = l.Close(context.Background())
		return n
	}
}

// wakes starts a run through from while a worker of to waits with nothing
// to do, three times, and fails unless each run ends well before the
// worker's one-second poll would have found it.
func wakes(t *testing.T, e *rt.Env, from, to *runtime.Store, what string) {
	t.Helper()
	w := e.Worker("w-" + what)
	w.Store, w.Drain = to, time.Second
	wctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = w.Run(wctx) }()
	defer func() { cancel(); wg.Wait() }()

	wf := e.Publish(t, wfDoc(payOne, ""))
	for i := 0; i < 3; i++ {
		time.Sleep(250 * time.Millisecond) // the worker has claimed, found nothing and is waiting
		began := time.Now()
		ref, _, err := from.StartRun(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod",
			Trigger: map[string]any{"amount": 100 + i}, Env: map[string]any{"api": "https://api.example"}})
		if err != nil {
			t.Fatal(err)
		}
		rt.WaitFor(t, 10*time.Second, "the run to complete", func() bool { return e.Status(t, ref) == "completed" })
		if took := time.Since(began); took > 600*time.Millisecond {
			t.Errorf("%s: run %d completed after %s: the worker was not woken and found it by polling", what, i+1, took)
		}
	}
}

// A task scheduled in one process wakes a waiting worker of another, with
// the wake-up sent after the commit (docs/spec/task-wake-ups.md).
func TestTaskWakesAnotherProcess(t *testing.T) {
	e := rt.New(t)
	wakes(t, e, process(t, e, false), process(t, e, false), "another process")
}

// A worker in the process that scheduled the task is woken too.
func TestTaskWakesItsOwnProcess(t *testing.T) {
	e := rt.New(t)
	s := process(t, e, false)
	wakes(t, e, s, s, "the same process")
}

// During a rolling update a process that still notifies from the trigger
// and one that notifies after the commit wake each other's workers: the
// channel and the payload are the same.
func TestMixedVersionsWake(t *testing.T) {
	e := rt.New(t)
	before, after := process(t, e, true), process(t, e, false)
	wakes(t, e, before, after, "old to new")
	wakes(t, e, after, before, "new to old")
}

// The engine's transactions no longer notify: runs started one after
// another are announced in far fewer notifications than there are runs,
// where the trigger sent one for each. With TASKIEM_WAKE_IN_TX the trigger
// does, as before.
func TestEngineCommitsDoNotNotify(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(payOne, ""))
	const runs = 40
	start := func(s *runtime.Store) int {
		heard := notifications(t, e)
		for i := 0; i < runs; i++ {
			if _, _, err := s.StartRun(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod",
				Trigger: map[string]any{"amount": 100 + i}, Env: map[string]any{"api": "https://api.example"}}); err != nil {
				t.Fatal(err)
			}
		}
		return heard()
	}
	s := process(t, e, false)
	s.WakeInterval = time.Second
	began := time.Now()
	got := start(s)
	if most := int(time.Since(began)/time.Second) + 2; got < 1 || got > most {
		t.Errorf("%d runs sent %d notifications, want between 1 and %d (one a second)", runs, got, most)
	}
	if got := start(process(t, e, true)); got != runs {
		t.Errorf("with wake-ups left in the transaction, %d runs sent %d notifications, want one each", runs, got)
	}
	e.Drain(t)
}
