package runtime_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	dto "github.com/prometheus/client_model/go"

	"github.com/israel-duff/taskiem/engine/runtime"
	"github.com/israel-duff/taskiem/engine/telemetry"
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
	// The first store may still owe its last wake-up, up to one interval
	// after its last run: let it go before counting the second store's.
	time.Sleep(s.WakeInterval + 300*time.Millisecond)
	if got := start(process(t, e, true)); got != runs {
		t.Errorf("with wake-ups left in the transaction, %d runs sent %d notifications, want one each", runs, got)
	}
	e.Drain(t)
}

// inTx reads how many tasks' wake-ups were left to the trigger inside
// their transaction, for the connector queue.
func inTx(t *testing.T) float64 {
	t.Helper()
	var m dto.Metric
	if err := telemetry.WakeupsInTx.WithLabelValues("connector").Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

// The transactions in which a worker records a step and schedules the next
// do not notify either: three steps in sequence, run by a worker, are
// announced by the notifier and never by the trigger. (The first build
// covered only the transaction that starts a run: the worker's go through
// InTenantTxRetry, whose transactions could not hold an after-commit
// function.)
func TestWorkerCommitsDoNotNotify(t *testing.T) {
	e := rt.New(t)
	step := func(id, needs string) string {
		s := strings.Replace(payOne, `"id":"pay",`, `"id":"`+id+`",`+needs, 1)
		return strings.Replace(s, `':pay'`, `':`+id+`'`, 1)
	}
	wf := e.Publish(t, wfDoc(step("a", "")+","+step("b", `"needs":["a"],`)+","+step("c", `"needs":["b"],`), ""))
	s := process(t, e, false)
	s.WakeInterval = time.Second
	w := e.Worker("w-commits")
	w.Store, w.Drain = s, time.Second
	wctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = w.Run(wctx) }()
	defer func() { cancel(); wg.Wait() }()

	const runs = 10
	left := inTx(t)
	heard := notifications(t, e)
	began := time.Now()
	var refs []runtime.RunRef
	for i := 0; i < runs; i++ {
		ref, _, err := s.StartRun(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod",
			Trigger: map[string]any{"amount": 100 + i}, Env: map[string]any{"api": "https://api.example"}})
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}
	for _, ref := range refs {
		rt.WaitFor(t, 20*time.Second, "the run to complete", func() bool { return e.Status(t, ref) == "completed" })
	}
	took := time.Since(began)
	got := heard()
	// The trigger would have sent one for each of the 30 tasks.
	if most := int(took/time.Second) + 3; got < 1 || got > most {
		t.Errorf("%d runs of three steps (%s) sent %d notifications, want between 1 and %d (one a second)", runs, took, got, most)
	}
	if n := inTx(t) - left; n != 0 {
		t.Errorf("%v tasks' wake-ups were left to the trigger inside their transaction, want none", n)
	}
	if n := e.Provider.Executions(refs[0].ID.String() + ":c"); n != 1 {
		t.Errorf("the last step of the first run moved money %d times, want 1", n)
	}
}
