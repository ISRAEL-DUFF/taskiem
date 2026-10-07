package runtime_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A worker rides out repeated connection drops (a primary failing over,
// decision 0024): its LISTEN connection comes back, outcomes already
// fetched are recorded once the database answers, and every payment runs
// to completion exactly once.
func TestWorkerSurvivesConnectionDrops(t *testing.T) {
	e := rt.New(t)
	px := e.DB.NewProxy(t)
	pool := px.Pool(t, e.DB, "taskiem_app", 8, nil)
	store := &runtime.Store{Pool: pool, Registry: e.Registry, PII: e.Vault}
	w := e.Worker("w-ha")
	w.Store, w.Lease, w.Drain = store, 3*time.Second, time.Second
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = w.Run(ctx) }()
	// The scheduler (on the primary directly) recovers leases lost with
	// a connection, as in production.
	go func() { defer wg.Done(); _ = e.Scheduler().Run(ctx) }()
	defer func() { cancel(); wg.Wait() }()

	listening := func() int {
		var n int
		if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND query = 'LISTEN taskiem_tasks'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	rt.WaitFor(t, 10*time.Second, "the worker to listen", func() bool { return listening() == 1 })

	wf := e.Publish(t, wfDoc(payOne, ""))
	var refs []runtime.RunRef
	for i := 0; i < 12; i++ {
		refs = append(refs, e.Start(t, wf, map[string]any{"amount": 100 + i}))
		if i%3 == 2 {
			px.Cut() // a failover in the middle of the work
		}
	}
	// A storm of drops, then quiet.
	for i := 0; i < 8; i++ {
		time.Sleep(150 * time.Millisecond)
		px.Cut()
	}
	rt.WaitFor(t, 60*time.Second, "every run to complete", func() bool {
		for _, r := range refs {
			if st, _ := e.Store.RunStatus(ctx, r); st != "completed" {
				return false
			}
		}
		return true
	})
	for _, r := range refs {
		if n := e.Provider.Executions(r.ID.String() + ":pay"); n != 1 {
			t.Errorf("run %s: payment executed %d times", r.ID, n)
		}
	}
	// LISTEN came back on a new connection.
	rt.WaitFor(t, 15*time.Second, "the worker to listen again", func() bool { return listening() == 1 })
	// And a run started now is picked up.
	late := e.Start(t, wf, map[string]any{"amount": 1})
	rt.WaitFor(t, 15*time.Second, "a run after the drops", func() bool { st, _ := e.Store.RunStatus(ctx, late); return st == "completed" })
}
