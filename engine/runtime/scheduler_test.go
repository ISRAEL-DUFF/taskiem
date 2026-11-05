package runtime_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// poison makes a run's decision fail every time (its expansion names no
// stored definition), standing in for a tenant whose key is unavailable or
// a decision that faults.
func poison(t *testing.T, e *rt.Env, run uuid.UUID) {
	t.Helper()
	e.NoReplay = true // the poisoned run cannot replay
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE runs SET expansion = '\xdeadbeef' WHERE id = $1`, run); err != nil {
		t.Fatal(err)
	}
}

// waiters starts n runs of a workflow that waits an hour and then
// finishes, in tenant.
func waiters(t *testing.T, e *rt.Env, tenant uuid.UUID, n int) []runtime.RunRef {
	t.Helper()
	wf := e.PublishIn(t, tenant, wfDoc(`{"id":"w","type":"wait","config":{"duration":"1h"}},{"id":"t","type":"transform","needs":["w"],"config":{"output":1}}`, ""))
	var refs []runtime.RunRef
	for i := 0; i < n; i++ {
		ref, _, err := e.Store.StartRun(ctx, runtime.StartRequest{TenantID: tenant, WorkflowID: wf, Version: 1, Environment: "prod", Trigger: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, ref)
	}
	return refs
}

// One run whose decision fails does not stop the timers claimed with it:
// the others fire in the same tick, the failing one is reported and put
// back for later rather than heading every batch, and it is not counted as
// fired.
func TestSchedulerIsolatesFailingTimer(t *testing.T) {
	e := rt.New(t)
	bad := waiters(t, e, e.Tenant, 1)[0]
	other := e.AddTenant(t)
	good := waiters(t, e, other, 3)
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE timers SET fire_at = now() - interval '5 minutes' WHERE fired_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE timers SET fire_at = now() - interval '10 minutes' WHERE run_id = $1`, bad.ID); err != nil {
		t.Fatal(err)
	}
	poison(t, e, bad.ID)
	s := e.Scheduler()
	st, err := s.Tick(ctx)
	if err == nil || !strings.Contains(err.Error(), bad.ID.String()) {
		t.Errorf("tick error %v, want one naming run %s", err, bad.ID)
	}
	if st.TimersFired != len(good) {
		t.Errorf("timers fired %d, want %d", st.TimersFired, len(good))
	}
	for _, ref := range good {
		if s := e.Status(t, ref); s != "completed" {
			t.Errorf("other tenant's run %s: %s", ref.ID, s)
		}
	}
	var backedOff bool
	if err := e.DB.Admin.QueryRow(ctx, `SELECT lease_until > now() + interval '45 seconds' FROM timers WHERE run_id = $1 AND fired_at IS NULL`, bad.ID).Scan(&backedOff); err != nil {
		t.Fatal(err)
	}
	if !backedOff {
		t.Error("the failing timer is claimable again after the usual lease")
	}
}

// The sweep goes on past a run it cannot decide, in the same way.
func TestSchedulerIsolatesFailingSweep(t *testing.T) {
	e := rt.New(t)
	bad := waiters(t, e, e.Tenant, 1)[0]
	other := e.AddTenant(t)
	good := waiters(t, e, other, 3)
	// Every run has an event no decision has covered yet.
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE runs SET decided_seq = decided_seq - 1`); err != nil {
		t.Fatal(err)
	}
	poison(t, e, bad.ID)
	st, err := e.Scheduler().Tick(ctx)
	if err == nil || !strings.Contains(err.Error(), bad.ID.String()) {
		t.Errorf("tick error %v, want one naming run %s", err, bad.ID)
	}
	if st.RunsSwept != len(good) {
		t.Errorf("runs swept %d, want %d", st.RunsSwept, len(good))
	}
	var undecided int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM runs WHERE tenant_id = $1 AND decided_seq < last_seq`, other).Scan(&undecided); err != nil {
		t.Fatal(err)
	}
	if undecided != 0 {
		t.Errorf("%d of the other tenant's runs left undecided", undecided)
	}
	var backedOff bool
	if err := e.DB.Admin.QueryRow(ctx, `SELECT orch_lease_until > now() + interval '45 seconds' FROM runs WHERE id = $1`, bad.ID).Scan(&backedOff); err != nil {
		t.Fatal(err)
	}
	if !backedOff {
		t.Error("the failing run is claimable again after the usual lease")
	}
}
