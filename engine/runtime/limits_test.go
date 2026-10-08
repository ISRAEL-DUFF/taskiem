package runtime_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/alerts"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

const waitHour = `{"id":"w","type":"wait","config":{"duration":"1h"}}`

func setLimits(t *testing.T, e *rt.Env, tenant uuid.UUID, values map[string]any) {
	t.Helper()
	if err := e.Store.SetLimits(ctx, tenant, values, "test"); err != nil {
		t.Fatal(err)
	}
}

func startIn(t *testing.T, e *rt.Env, tenant, wf uuid.UUID, throttled bool, dedup string) runtime.Started {
	t.Helper()
	st, err := e.Store.Start(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: 1, Environment: "prod",
		Trigger: map[string]any{}, Env: map[string]any{}, Throttled: throttled, TriggerID: "test", DedupKey: dedup})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// setTokens fills or empties a tenant's admission bucket, as time would.
func setTokens(t *testing.T, e *rt.Env, tenant uuid.UUID, tokens float64) {
	t.Helper()
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO tenant_admission (tenant_id, tokens, refilled_at) VALUES ($1, $2, now())
		ON CONFLICT (tenant_id) DO UPDATE SET tokens = $2, refilled_at = now()`, tenant, tokens); err != nil {
		t.Fatal(err)
	}
}

func statuses(t *testing.T, e *rt.Env, refs []runtime.Started) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = e.Status(t, r.Ref)
	}
	return out
}

// Above the soft rate runs are recorded queued, later starts keep their
// place behind them, and the scheduler admits them in order at the
// tenant's rate. A new process (a restart) carries on from the database.
func TestSoftLimitQueuesThenDrainsInOrder(t *testing.T) {
	e := rt.New(t)
	setLimits(t, e, e.Tenant, map[string]any{"ingest_rate": 1, "ingest_burst": 2})
	wf := e.Publish(t, wfDoc(waitHour, ""))
	var runs []runtime.Started
	for i := 0; i < 4; i++ {
		st := startIn(t, e, e.Tenant, wf, true, "")
		if !st.Queued || st.HeldBy != "ingest_rate" {
			t.Fatalf("throttled start %d: %+v", i, st)
		}
		runs = append(runs, st)
	}
	// Not throttled itself, but it may not overtake the runs already waiting.
	late := startIn(t, e, e.Tenant, wf, false, "")
	if !late.Queued || late.HeldBy != "backlog" {
		t.Fatalf("late start should queue behind the backlog: %+v", late)
	}
	runs = append(runs, late)
	if h := events(t, e, runs[0].Ref); len(h) != 1 || h[0].Type != history.RunStarted {
		t.Fatalf("a queued run made progress: %s", types(h))
	}

	setTokens(t, e, e.Tenant, 2)
	n, err := e.Store.AdmitQueued(ctx)
	if err != nil || n != 2 {
		t.Fatalf("first admission: %d %v", n, err)
	}
	want := []string{"running", "running", "queued", "queued", "queued"}
	if got := statuses(t, e, runs); !equal(got, want) {
		t.Fatalf("after one admission %v, want %v", got, want)
	}
	if n, _ := e.Store.AdmitQueued(ctx); n != 0 {
		t.Fatalf("admitted %d with an empty bucket", n)
	}

	// A restart: another process's store, nothing in memory.
	fresh := &runtime.Store{Pool: e.DB.App, Registry: e.Registry, PII: e.Vault}
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE tenant_admission SET refilled_at = now() - interval '1 second' WHERE tenant_id = $1`, e.Tenant); err != nil {
		t.Fatal(err)
	}
	if n, err := fresh.AdmitQueued(ctx); err != nil || n != 1 {
		t.Fatalf("after a second at 1/s: %d %v", n, err)
	}
	setTokens(t, e, e.Tenant, 2)
	if n, _ := fresh.AdmitQueued(ctx); n != 2 {
		t.Fatalf("drain: %d", n)
	}
	for i, r := range runs {
		h := events(t, e, r.Ref)
		if e.Status(t, r.Ref) != "running" || count(h, history.RunAdmitted, "") != 1 || count(h, history.StepScheduled, "w") != 1 {
			t.Errorf("run %d: %s %s", i, e.Status(t, r.Ref), types(h))
		}
	}
	// In order: each run was admitted no later than the one after it.
	var prev time.Time
	for i, r := range runs {
		var at time.Time
		if err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT recorded_at FROM run_events WHERE run_id = $1 AND type = 'RunAdmitted'`, r.Ref.ID).Scan(&at)
		}); err != nil {
			t.Fatal(err)
		}
		if at.Before(prev) {
			t.Errorf("run %d admitted before run %d", i, i-1)
		}
		prev = at
	}
	// Once the backlog is gone, starts under the rate begin at once.
	if st := startIn(t, e, e.Tenant, wf, false, ""); st.Queued {
		t.Errorf("start after the backlog drained was queued: %+v", st)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A full backlog refuses new starts and records nothing; a retry of a
// delivery already queued is recognised, not counted again.
func TestBacklogCapAndDedupWhileQueued(t *testing.T) {
	e := rt.New(t)
	setLimits(t, e, e.Tenant, map[string]any{"max_queued_runs": 2, "runs_per_day": 10})
	wf := e.Publish(t, wfDoc(waitHour, ""))
	a := startIn(t, e, e.Tenant, wf, true, "order-1")
	startIn(t, e, e.Tenant, wf, true, "order-2")
	again := startIn(t, e, e.Tenant, wf, true, "order-1")
	if again.Created || again.Ref.ID != a.Ref.ID {
		t.Fatalf("retry while queued: %+v, first %s", again, a.Ref.ID)
	}
	_, err := e.Store.Start(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod",
		Trigger: map[string]any{}, Env: map[string]any{}, Throttled: true, TriggerID: "test", DedupKey: "order-3"})
	le, ok := runtime.IsLimit(err)
	if !ok || le.Code != "backlog_full" || le.Limit != "max_queued_runs" || le.RetryAfter <= 0 {
		t.Fatalf("third start: %v", err)
	}
	// Refused before acceptance: no run, no receipt, so the provider's
	// retry after the backlog drains starts it.
	v, err := e.Store.ViewLimits(ctx, e.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	if v.Usage.QueuedRuns != 2 || v.Usage.RunsToday != 2 {
		t.Errorf("usage %+v", v.Usage)
	}
	hit := map[string]bool{}
	for _, h := range v.Hits {
		hit[h.Limit] = true
	}
	if !hit["max_queued_runs"] || !hit["ingest_rate"] {
		t.Errorf("hits %+v", v.Hits)
	}
	if _, err := e.Store.AdmitQueued(ctx); err != nil {
		t.Fatal(err)
	}
	if st := startIn(t, e, e.Tenant, wf, false, "order-3"); !st.Created {
		t.Errorf("retry after the backlog drained: %+v", st)
	}
}

// Beyond the day's quota starts are refused with a clear code, the hit is
// recorded once, and a "limit" alert rule tells the tenant.
func TestQuotaRefusesAndAlerts(t *testing.T) {
	e := rt.New(t)
	setLimits(t, e, e.Tenant, map[string]any{"runs_per_day": 2})
	wf := e.Publish(t, wfDoc(waitHour, ""))
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		ch := uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `INSERT INTO alert_channels (id, tenant_id, kind, name, config, created_by) VALUES ($1, $2, 'email', 'ops', '{"to":["ops@x.test"]}', 'test')`, ch, e.Tenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO alert_rules (id, tenant_id, name, kind, channel_ids, created_by, checked_until) VALUES ($1, $2, 'limits', 'limit', $3, 'test', now() - interval '1 minute')`,
			uuid.Must(uuid.NewV7()), e.Tenant, []uuid.UUID{ch})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	startIn(t, e, e.Tenant, wf, false, "")
	startIn(t, e, e.Tenant, wf, false, "")
	for i := 0; i < 3; i++ {
		_, err := e.Store.Start(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: map[string]any{}, Env: map[string]any{}})
		le, ok := runtime.IsLimit(err)
		if !ok || le.Code != "quota_exceeded" || le.Limit != "runs_per_day" || le.RetryAfter <= 0 || le.RetryAfter > 24*time.Hour {
			t.Fatalf("start beyond the quota: %v", err)
		}
		if !errors.As(err, &le) || le.Message == "" {
			t.Fatal("no message")
		}
	}
	al := &alerts.Alerter{Pool: e.DB.App}
	if err := al.Evaluate(ctx, e.Tenant); err != nil {
		t.Fatal(err)
	}
	if err := al.Evaluate(ctx, e.Tenant); err != nil {
		t.Fatal(err)
	}
	var n int
	var title string
	err = db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*), max(title) FROM alerts WHERE kind = 'limit'`).Scan(&n, &title)
	})
	if err != nil || n != 1 || title != "Plan limit reached: runs_per_day" {
		t.Fatalf("limit alerts: %d %q %v", n, title, err)
	}
	// Raising the quota lets runs start again (after the cache, at once here).
	setLimits(t, e, e.Tenant, map[string]any{"runs_per_day": nil})
	startIn(t, e, e.Tenant, wf, false, "")
}

// Starts are counted in shards of the day's usage (migration 00161):
// concurrent starts are all counted, the limits page and the quota see
// their sum, and the quota still refuses at the limit.
func TestUsageShardsCountEveryStart(t *testing.T) {
	e := rt.New(t)
	wf := e.Publish(t, wfDoc(waitHour, ""))
	const n = 40
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := e.Store.Start(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: map[string]any{}, Env: map[string]any{}})
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	var rows, total int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*), sum(runs_started) FROM tenant_usage WHERE tenant_id = $1`, e.Tenant).Scan(&rows, &total); err != nil {
		t.Fatal(err)
	}
	if total != n || rows < 2 {
		t.Fatalf("%d starts counted in %d rows", total, rows)
	}
	v, err := e.Store.ViewLimits(ctx, e.Tenant)
	if err != nil || v.Usage.RunsToday != n {
		t.Fatalf("limits page: %v %+v", err, v.Usage)
	}
	setLimits(t, e, e.Tenant, map[string]any{"runs_per_day": n + 1})
	startIn(t, e, e.Tenant, wf, false, "")
	if _, err := e.Store.Start(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: map[string]any{}, Env: map[string]any{}}); err == nil {
		t.Fatal("a start beyond the quota was accepted")
	}
}

// Runs beyond max_running_runs wait and start as running ones end.
func TestMaxRunningRunsQueues(t *testing.T) {
	e := rt.New(t)
	setLimits(t, e, e.Tenant, map[string]any{"max_running_runs": 1})
	wf := e.Publish(t, wfDoc(waitHour, ""))
	a := startIn(t, e, e.Tenant, wf, false, "")
	b := startIn(t, e, e.Tenant, wf, false, "")
	if a.Queued || !b.Queued || b.HeldBy != "max_running_runs" {
		t.Fatalf("a %+v b %+v", a, b)
	}
	if n, _ := e.Store.AdmitQueued(ctx); n != 0 {
		t.Fatalf("admitted %d while at the cap", n)
	}
	if err := e.Store.CancelRun(ctx, a.Ref, "u"); err != nil {
		t.Fatal(err)
	}
	if n, err := e.Store.AdmitQueued(ctx); n != 1 || err != nil {
		t.Fatalf("after a ended: %d %v", n, err)
	}
	if e.Status(t, b.Ref) != "running" {
		t.Errorf("b %s", e.Status(t, b.Ref))
	}
}

// A run that schedules more steps than its plan allows fails, and none of
// the steps beyond the cap is left for a worker.
func TestMaxStepsPerRunFailsRun(t *testing.T) {
	e := rt.New(t)
	setLimits(t, e, e.Tenant, map[string]any{"max_steps_per_run": 3})
	wf := e.Publish(t, wfDoc(`{"id":"fan","type":"foreach","config":{"items":"=[0,1,2,3,4,5,6,7,8,9]","steps":[`+waitHour+`]}}`, ""))
	ref := e.Start(t, wf, map[string]any{})
	if st := e.Status(t, ref); st != "failed" {
		t.Fatalf("status %s: %s", st, types(events(t, e, ref)))
	}
	h := events(t, e, ref)
	last := h[len(h)-1]
	if last.Type != history.RunFailed || last.Origin != history.OriginScheduler {
		t.Fatalf("last event %s (%s)", last.Type, last.Origin)
	}
	var timers int
	err := db.InTenantTx(ctx, e.DB.App, []uuid.UUID{e.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM timers WHERE run_id = $1 AND fired_at IS NULL`, ref.ID).Scan(&timers)
	})
	if err != nil || timers != 0 {
		t.Errorf("timers left: %d %v", timers, err)
	}
	// Under the cap a run is untouched.
	setLimits(t, e, e.Tenant, map[string]any{"max_steps_per_run": 100})
	if ok := e.Start(t, wf, map[string]any{}); e.Status(t, ok) != "running" {
		t.Errorf("under the cap: %s", e.Status(t, ok))
	}
}

// One tenant's flood does not hold up another's admissions: each tenant
// has its own bucket and every tick looks at every tenant with a backlog.
func TestAdmissionFairAcrossTenants(t *testing.T) {
	e := rt.New(t)
	other := e.AddTenant(t)
	setLimits(t, e, e.Tenant, map[string]any{"ingest_rate": 5, "ingest_burst": 5})
	setLimits(t, e, other, map[string]any{"ingest_rate": 5, "ingest_burst": 5})
	wfA := e.Publish(t, wfDoc(waitHour, ""))
	wfB := e.PublishIn(t, other, wfDoc(waitHour, ""))
	for i := 0; i < 60; i++ {
		startIn(t, e, e.Tenant, wfA, true, "")
	}
	b := startIn(t, e, other, wfB, true, "")
	setTokens(t, e, e.Tenant, 5)
	setTokens(t, e, other, 5)
	n, err := e.Store.AdmitQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status(t, b.Ref) != "running" {
		t.Fatal("tenant B's run waited behind tenant A's flood")
	}
	if n != 6 { // A's five tokens' worth, and B's one
		t.Errorf("admitted %d, want 6", n)
	}
}
