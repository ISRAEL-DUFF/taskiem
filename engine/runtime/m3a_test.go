package runtime_test

import (
	"compress/gzip"
	"encoding/json"
	"errors"
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
	// The notifier is told, once per run (no-technical-steps N12).
	var told int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM run_outcomes WHERE run_id IN ($1, $2) AND status = 'needs_reconciliation'`, done.ID, retry.ID).Scan(&told); err != nil || told != 2 {
		t.Fatalf("run outcomes: %d %v", told, err)
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

// intentsOf returns the EffectIntent payloads recorded for one step.
func intentsOf(t *testing.T, e *rt.Env, ref runtime.RunRef, step string) []history.IntentPayload {
	t.Helper()
	var out []history.IntentPayload
	for _, ev := range events(t, e, ref) {
		if ev.Type == history.EffectIntent && ev.StepID == step {
			var ip history.IntentPayload
			if err := json.Unmarshal(ev.Payload, &ip); err != nil {
				t.Fatal(err)
			}
			out = append(out, ip)
		}
	}
	return out
}

// An operator's retry is a person re-issuing the effect: the provider gets
// a new key, not the one it may have cached a failure for (decision 0053).
func TestOperatorRetryReissuesKey(t *testing.T) {
	e := rt.New(t)
	faulty := true
	e.Provider.Faults = func(string) rt.Fault {
		if faulty {
			return rt.FailAfter
		}
		return rt.NoFault
	}
	wf := e.Publish(t, wfDoc(`{"id":"pay","type":"connector","connector":"fakepay@1","action":"transfer","retry":{"max":0},"input":{"amount":1,"logical_id":"=run.id"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	e.Drain(t)
	if st := e.Status(t, ref); st != "needs_reconciliation" {
		t.Fatalf("setup: %s %s", st, types(events(t, e, ref)))
	}
	faulty = false
	if err := e.Store.ResolveStep(ctx, ref, "pay", runtime.ResolveRetry, nil, "provider shows the transfer failed: insufficient balance", "ops"); err != nil {
		t.Fatal(err)
	}
	rt.WaitFor(t, 10*time.Second, "retried run", func() bool { e.Drain(t); return e.Status(t, ref) != "running" })
	if st := e.Status(t, ref); st != "completed" {
		for _, ev := range events(t, e, ref) {
			t.Logf("%s(%s)#%d %s", ev.Type, ev.StepID, ev.Attempt, ev.Payload)
		}
		t.Fatalf("after retry: %s %s", st, types(events(t, e, ref)))
	}
	in := intentsOf(t, e, ref, "pay")
	if len(in) != 2 || in[0].AttemptGroup != 0 || in[1].AttemptGroup != 1 || in[0].Key == in[1].Key {
		t.Fatalf("intents: %+v", in)
	}
	// The provider saw the new reference, so it acted on the re-issue
	// instead of answering from the first reference's record.
	if n := e.Provider.Executions(ref.ID.String()); n != 2 {
		t.Errorf("provider executions: %d, want 2", n)
	}
}

// A worker that dies after recording an unsafe write's intent leaves the
// step parked; an operator's retry sends it, under a new key, instead of
// parking it again over the old intent.
func TestOperatorRetryAfterCrashSends(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(`{"id":"sms","type":"connector","connector":"fakepay@1","action":"notify","input":{"logical_id":"=run.id"}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	w := e.Worker("crashy")
	w.Lease = 300 * time.Millisecond
	w.Hooks = &runtime.Hooks{AfterIntent: func(string, int) error { return errors.New("crash after intent") }}
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // the lease expires
	e.Drain(t)
	if st := e.Status(t, ref); st != "needs_reconciliation" || e.Provider.Executions(ref.ID.String()) != 0 {
		t.Fatalf("setup: %s executions=%d %s", st, e.Provider.Executions(ref.ID.String()), types(events(t, e, ref)))
	}
	if err := e.Store.ResolveStep(ctx, ref, "sms", runtime.ResolveRetry, nil, "not in provider logs", "ops"); err != nil {
		t.Fatal(err)
	}
	rt.WaitFor(t, 10*time.Second, "retried run", func() bool { e.Drain(t); return e.Status(t, ref) != "running" })
	if st := e.Status(t, ref); st != "completed" || e.Provider.Executions(ref.ID.String()) != 1 {
		t.Fatalf("after retry: %s executions=%d %s", st, e.Provider.Executions(ref.ID.String()), types(events(t, e, ref)))
	}
	if in := intentsOf(t, e, ref, "sms"); len(in) != 2 || in[1].AttemptGroup != 1 {
		t.Errorf("intents: %+v", in)
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
	var purges int
	_ = e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'run.purge' AND target = $2 AND actor_type = 'system'`,
		e.Tenant, short.ID.String()).Scan(&purges)
	if purges != 1 {
		t.Errorf("%d run.purge audit entries, want 1", purges)
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
