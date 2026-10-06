package runtime_test

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/engine/decide"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
	"github.com/israel-duff/taskiem/engine/wd"
)

func TestConcurrencyKeySerialisesRuns(t *testing.T) {
	e := rt.New(t)
	doc := wfDoc(`{"id":"ok","type":"approval","config":{"role":"officer"}}`, `{"concurrency_key":"=trigger.borrower"}`)
	wf := e.Publish(t, doc)
	a := e.Start(t, wf, map[string]any{"borrower": "b1"})
	b := e.Start(t, wf, map[string]any{"borrower": "b1"})
	c := e.Start(t, wf, map[string]any{"borrower": "b2"})
	if e.Status(t, a) != "running" || e.Status(t, b) != "queued" || e.Status(t, c) != "running" {
		t.Fatalf("statuses %s %s %s", e.Status(t, a), e.Status(t, b), e.Status(t, c))
	}
	e.Drain(t) // the sweep must not decide a queued run
	if count(events(t, e, b), history.ApprovalRequested, "") != 0 {
		t.Fatal("queued run made progress")
	}
	if err := e.Store.DecideApproval(ctx, a, "ok", "approved", "u", "web"); err != nil {
		t.Fatal(err)
	}
	if e.Status(t, a) != "completed" || e.Status(t, b) != "running" {
		t.Fatalf("after a: %s %s", e.Status(t, a), e.Status(t, b))
	}
	h := events(t, e, b)
	if count(h, history.RunAdmitted, "") != 1 || count(h, history.ApprovalRequested, "ok") != 1 {
		t.Errorf("b should be admitted and waiting: %s", types(h))
	}
	def, _ := wd.Load([]byte(doc))
	if err := decide.Verify(def, h); err != nil {
		t.Errorf("replay: %v", err)
	}
}

func TestMaxConcurrencyQueuesAndCancelPromotes(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"w","type":"wait","config":{"duration":"1h"}}`, `{"max_concurrency":1}`))
	a := e.Start(t, wf, map[string]any{})
	b := e.Start(t, wf, map[string]any{})
	if e.Status(t, b) != "queued" {
		t.Fatalf("b %s", e.Status(t, b))
	}
	if err := e.Store.CancelRun(ctx, a, "u"); err != nil {
		t.Fatal(err)
	}
	if e.Status(t, b) != "running" {
		t.Errorf("b should be admitted once a is cancelled: %s", e.Status(t, b))
	}
}

func TestOperatorResolvesParkedStep(t *testing.T) {
	e := rt.New(t)
	faulty := true
	e.Provider.Faults = func(string) rt.Fault {
		if faulty {
			return rt.FailAfter
		}
		return rt.NoFault
	}
	wf := e.Publish(t, wfDoc(`{"id":"sms","type":"connector","connector":"fakepay@1","action":"notify","input":{"logical_id":"=run.id"}},
	  {"id":"after","type":"transform","needs":["sms"],"config":{"output":"=steps.sms.output.sent"}}`, ""))
	done := e.Start(t, wf, map[string]any{})
	retry := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if e.Status(t, done) != "needs_reconciliation" || e.Status(t, retry) != "needs_reconciliation" {
		t.Fatal("setup: both runs should park")
	}
	if err := e.Store.ResolveStep(ctx, done, "after", runtime.ResolveCompleted, nil, "", "ops"); err == nil {
		t.Error("resolving a step that is not parked should fail")
	}
	if err := e.Store.ResolveStep(ctx, done, "sms", runtime.ResolveCompleted, map[string]any{"sent": true}, "Termii dashboard shows delivered", "ops"); err != nil {
		t.Fatal(err)
	}
	if st := e.Status(t, done); st != "completed" {
		t.Errorf("resolved completed: %s %s", st, types(events(t, e, done)))
	}
	faulty = false
	if err := e.Store.ResolveStep(ctx, retry, "sms", runtime.ResolveRetry, nil, "not in provider logs", "ops"); err != nil {
		t.Fatal(err)
	}
	rt.WaitFor(t, 10*time.Second, "retried run", func() bool { e.Drain(t); return e.Status(t, retry) != "running" })
	if st := e.Status(t, retry); st != "completed" || e.Provider.Executions(retry.ID.String()) != 2 {
		t.Errorf("resolved retry: %s executions=%d", st, e.Provider.Executions(retry.ID.String()))
	}
	var audits int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'run.resolve'`).Scan(&audits)
	if audits != 2 {
		t.Errorf("audit entries %d", audits)
	}
}

func TestRetentionArchivesThenPurges(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"x","type":"transform","config":{"output":1}}`, `{"retention":"1s"}`))
	short := e.Start(t, wf, map[string]any{})
	long := e.Start(t, e.Publish(t, wfDoc(`{"id":"x","type":"transform","config":{"output":1}}`, "")), map[string]any{})
	time.Sleep(1200 * time.Millisecond)
	dir := t.TempDir()
	s := e.Scheduler()
	if n, err := s.PurgeExpired(ctx, 10); err != nil || n != 0 {
		t.Fatalf("without an archiver nothing is purged: %d %v", n, err)
	}
	s.Archiver = runtime.FileArchiver{Dir: dir}
	n, err := s.PurgeExpired(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("purged %d: %v", n, err)
	}
	if _, err := e.Store.RunStatus(ctx, short); err == nil {
		t.Error("expired run still present")
	}
	if e.Status(t, long) != "completed" {
		t.Error("run within retention was purged")
	}
	var left int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE run_id = $1`, short.ID).Scan(&left)
	f, err := os.Open(filepath.Join(dir, e.Tenant.String(), short.ID.String()+".jsonl.gz"))
	if err != nil || left != 0 {
		t.Fatalf("archive %v, %d events left", err, left)
	}
	defer func() { _ = f.Close() }()
	if _, err := gzip.NewReader(f); err != nil {
		t.Errorf("archive unreadable: %v", err)
	}
}

func TestRetentionPurgesApprovals(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"ok","type":"approval","config":{"role":"officer"}}`, `{"retention":"1s"}`))
	ref := e.Start(t, wf, map[string]any{})
	if err := e.Store.DecideApproval(ctx, ref, "ok", "approved", "checker", "web"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	s := e.Scheduler()
	s.Archiver = runtime.FileArchiver{Dir: t.TempDir()}
	if n, err := s.PurgeExpired(ctx, 10); err != nil || n != 1 {
		t.Fatalf("purged %d: %v", n, err)
	}
	var left int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE run_id = $1`, ref.ID).Scan(&left)
	if left != 0 {
		t.Errorf("%d approvals left", left)
	}
}

// A tenant's default retention applies to workflows that set none; a
// workflow's own setting still wins.
func TestTenantDefaultRetention(t *testing.T) {
	e := rt.New(t)
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO governance_settings (tenant_id, default_retention, updated_by) VALUES ($1, '30d', 'test')`, e.Tenant); err != nil {
		t.Fatal(err)
	}
	plain := e.Start(t, e.Publish(t, wfDoc(`{"id":"x","type":"transform","config":{"output":1}}`, "")), map[string]any{})
	own := e.Start(t, e.Publish(t, wfDoc(`{"id":"x","type":"transform","config":{"output":1}}`, `{"retention":"7d"}`)), map[string]any{})
	days := func(ref runtime.RunRef) float64 {
		var d float64
		if err := e.DB.Admin.QueryRow(ctx, `SELECT extract(epoch FROM retain_until - ended_at) / 86400 FROM runs WHERE id = $1`, ref.ID).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := days(plain); d < 29.9 || d > 30.1 {
		t.Errorf("tenant default: %.1f days", d)
	}
	if d := days(own); d < 6.9 || d > 7.1 {
		t.Errorf("workflow setting: %.1f days", d)
	}
}
