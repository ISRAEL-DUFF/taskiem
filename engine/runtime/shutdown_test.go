package runtime_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// Spec 15.4: on shutdown a worker claims nothing new, lets in-flight steps
// finish for up to its drain, and then releases the leases of the steps it
// had to cut off, so another worker takes them at once instead of after
// the lease expires.
func TestWorkerDrainsThenReleasesLeases(t *testing.T) {
	e := rt.New(t)
	var hang atomic.Bool
	hang.Store(true)
	started := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" && hang.Load() {
			started <- struct{}{}
			<-r.Context().Done() // until the worker gives up
			return
		}
		if r.URL.Path == "/quick" {
			started <- struct{}{}
			time.Sleep(150 * time.Millisecond) // finishes within the drain
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	if err := e.Store.AllowEgress(ctx, e.Tenant, "prod", "127.0.0.1", "admin"); err != nil {
		t.Fatal(err)
	}
	slow := e.Start(t, e.Publish(t, wfDoc(`{"id":"get","type":"http","retry":{"max":0},"config":{"method":"GET","url":"`+srv.URL+`/slow"}}`, "")), map[string]any{})
	quick := e.Start(t, e.Publish(t, wfDoc(`{"id":"get","type":"http","retry":{"max":0},"config":{"method":"GET","url":"`+srv.URL+`/quick"}}`, "")), map[string]any{})

	w := e.Worker("w-shutdown")
	w.Lease = 10 * time.Minute // so only a release, not expiry, frees it
	w.CallTimeout = 10 * time.Second
	w.Drain = 600 * time.Millisecond
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("steps did not start")
		}
	}
	cancel()
	begun := time.Now()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not stop")
	}
	if took := time.Since(begun); took < 500*time.Millisecond || took > 5*time.Second {
		t.Errorf("stopped after %s (drain 600ms)", took)
	}

	// The quick step finished during the drain; the slow one was released.
	var leased, free int
	if err := e.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE lease_owner IS NOT NULL), count(*) FILTER (WHERE lease_owner IS NULL)
		FROM tasks WHERE run_id = $1`, slow.ID).Scan(&leased, &free); err != nil {
		t.Fatal(err)
	}
	if leased != 0 || free != 1 {
		t.Fatalf("slow step's task: %d leased, %d free", leased, free)
	}
	hang.Store(false)
	e.Drain(t)
	if st := e.Status(t, slow); st != "completed" {
		t.Errorf("slow run after release: %s", st)
	}
	if st := e.Status(t, quick); st != "completed" {
		t.Errorf("quick run: %s", st)
	}
}
