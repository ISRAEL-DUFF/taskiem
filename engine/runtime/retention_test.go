package runtime_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

// countingArchiver records every archive call, slowly (so two schedulers
// overlap), and fails for the runs in poison.
type countingArchiver struct {
	mu     sync.Mutex
	calls  map[uuid.UUID]int
	poison map[uuid.UUID]bool
	delay  time.Duration
}

func (a *countingArchiver) Archive(_ context.Context, _, run uuid.UUID, _ []history.Event) error {
	time.Sleep(a.delay)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.calls == nil {
		a.calls = map[uuid.UUID]int{}
	}
	a.calls[run]++
	if a.poison[run] {
		return errors.New("poisoned run: bucket refused the object")
	}
	return nil
}

func (a *countingArchiver) heal() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.poison = nil
}

// expiredRuns starts n short-retention runs and moves them past retention.
func expiredRuns(t *testing.T, e *rt.Env, n int) []runtime.RunRef {
	t.Helper()
	wf := e.Publish(t, wfDoc(`{"id":"x","type":"transform","config":{"output":1}}`, `{"retention":"1d"}`))
	var refs []runtime.RunRef
	for range n {
		ref := e.Start(t, wf, map[string]any{})
		expire(t, e, ref)
		refs = append(refs, ref)
	}
	return refs
}

func remaining(t *testing.T, e *rt.Env, refs []runtime.RunRef) int {
	t.Helper()
	ids := make([]uuid.UUID, len(refs))
	for i, r := range refs {
		ids[i] = r.ID
	}
	var n int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE id = ANY ($1)`, ids).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func metricValue(t *testing.T, m prometheus.Metric) float64 {
	t.Helper()
	var d dto.Metric
	if err := m.Write(&d); err != nil {
		t.Fatal(err)
	}
	if d.Gauge != nil {
		return d.GetGauge().GetValue()
	}
	return d.GetCounter().GetValue()
}

// A backlog larger than a batch is purged in one tick: the pass loops
// while batches come back full (it used to purge one batch per 10 minutes).
func TestRetentionPurgeLoopsWhileBatchesAreFull(t *testing.T) {
	e := rt.New(t)
	refs := expiredRuns(t, e, 12)
	s := e.Scheduler()
	s.Archiver = &countingArchiver{}
	s.PurgeBatch = 5
	s.PurgeBudget = time.Minute
	before := metricValue(t, telemetry.RunsPurged)
	if _, err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := remaining(t, e, refs); n != 0 {
		t.Fatalf("%d of 12 expired runs left after one tick with batches of 5", n)
	}
	if got := metricValue(t, telemetry.RunsPurged) - before; got != 12 {
		t.Errorf("taskiem_runs_purged_total rose by %v, want 12", got)
	}
}

// A pass stops at its budget and carries on at the next tick, rather than
// waiting 10 minutes.
func TestRetentionPurgeResumesAfterBudget(t *testing.T) {
	e := rt.New(t)
	refs := expiredRuns(t, e, 6)
	s := e.Scheduler()
	s.Archiver = &countingArchiver{delay: 30 * time.Millisecond}
	s.PurgeBatch = 2
	s.PurgeBudget = time.Millisecond // one batch per tick
	for i := 0; i < 20 && remaining(t, e, refs) > 0; i++ {
		if _, err := s.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := remaining(t, e, refs); n != 0 {
		t.Fatalf("%d of 6 expired runs left after 20 ticks", n)
	}
}

// Two schedulers purging at once split the runs: none is archived twice.
func TestRetentionPurgeSchedulersShareWork(t *testing.T) {
	e := rt.New(t)
	refs := expiredRuns(t, e, 20)
	a := &countingArchiver{delay: 10 * time.Millisecond}
	var wg sync.WaitGroup
	purged := make([]int, 2)
	errs := make([]error, 2)
	for i := range 2 {
		s := e.Scheduler()
		s.ID = "sched-" + string(rune('a'+i))
		s.Archiver = a
		wg.Add(1)
		go func() {
			defer wg.Done()
			purged[i], errs[i] = s.PurgeExpired(ctx, 20)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if purged[0]+purged[1] != 20 || remaining(t, e, refs) != 0 {
		t.Errorf("purged %v, %d left", purged, remaining(t, e, refs))
	}
	for run, n := range a.calls {
		if n != 1 {
			t.Errorf("run %s archived %d times", run, n)
		}
	}
	if len(a.calls) != 20 {
		t.Errorf("%d runs archived, want 20", len(a.calls))
	}
}

// A run that cannot be archived is skipped and backed off, so the runs
// behind it are purged; it is retried once its backoff passes.
func TestRetentionPurgeSkipsAFailingRun(t *testing.T) {
	e := rt.New(t)
	refs := expiredRuns(t, e, 3)
	bad := refs[0]
	// Oldest first: the claim is ordered by retain_until.
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE runs SET retain_until = now() - interval '1 hour' WHERE id = $1`, bad.ID); err != nil {
		t.Fatal(err)
	}
	a := &countingArchiver{poison: map[uuid.UUID]bool{bad.ID: true}}
	s := e.Scheduler()
	s.Archiver = a
	failures := metricValue(t, telemetry.PurgeFailures)
	n, err := s.PurgeExpired(ctx, 10)
	if err != nil || n != 2 {
		t.Fatalf("purged %d (%v), want the 2 runs behind the failing one", n, err)
	}
	if got := metricValue(t, telemetry.PurgeFailures) - failures; got != 1 {
		t.Errorf("taskiem_retention_purge_failures_total rose by %v, want 1", got)
	}
	var attempts int
	var msg string
	var after time.Time
	if err := e.DB.Admin.QueryRow(ctx, `SELECT purge_attempts, purge_error, purge_after FROM runs WHERE id = $1`, bad.ID).Scan(&attempts, &msg, &after); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || !strings.Contains(msg, "bucket refused") || time.Until(after) < 30*time.Second {
		t.Errorf("failure recorded as attempts=%d error=%q retry in %s", attempts, msg, time.Until(after))
	}
	// Backed off: not claimed again at once.
	if n, err := s.PurgeExpired(ctx, 10); err != nil || n != 0 || a.calls[bad.ID] != 1 {
		t.Fatalf("retried at once: purged %d (%v), %d archive attempts", n, err, a.calls[bad.ID])
	}
	// The backoff passes and the archive recovers.
	a.heal()
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE runs SET purge_after = now() - interval '1 second' WHERE id = $1`, bad.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PurgeExpired(ctx, 10); err != nil || n != 1 || remaining(t, e, refs) != 0 {
		t.Fatalf("retry: purged %d (%v), %d left", n, err, remaining(t, e, refs))
	}
}

// The purge backlog is exported: runs past retention not yet purged, the
// oldest's wait, and how many keep failing.
func TestRetentionPurgeReportsBacklog(t *testing.T) {
	e := rt.New(t)
	refs := expiredRuns(t, e, 3)
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE runs SET retain_until = now() - interval '2 hours' WHERE id = $1`, refs[0].ID); err != nil {
		t.Fatal(err)
	}
	poison := map[uuid.UUID]bool{}
	for _, r := range refs {
		poison[r.ID] = true
	}
	s := e.Scheduler()
	s.Archiver = &countingArchiver{poison: poison}
	if _, err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := metricValue(t, telemetry.PurgeBacklog); got != 3 {
		t.Errorf("backlog %v, want 3", got)
	}
	if got := metricValue(t, telemetry.PurgeOldestOverdue); got < 7200 || got > 7300 {
		t.Errorf("oldest overdue %vs, want about 7200", got)
	}
	if got := metricValue(t, telemetry.PurgeFailing); got != 3 {
		t.Errorf("failing %v, want 3", got)
	}
	// A healthy archive clears it.
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE runs SET purge_after = NULL`); err != nil {
		t.Fatal(err)
	}
	s = e.Scheduler()
	s.Archiver = &countingArchiver{}
	if _, err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if b, o := metricValue(t, telemetry.PurgeBacklog), metricValue(t, telemetry.PurgeOldestOverdue); b != 0 || o != 0 {
		t.Errorf("after purging: backlog %v, oldest overdue %v", b, o)
	}
}
