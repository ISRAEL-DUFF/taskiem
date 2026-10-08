package runtime_test

import (
	"testing"

	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// A sub-tenant inherits container minutes from its partner and can only
// be lowered: a partner without them (0, off) turns them off below it.
func TestContainerLimitsInherit(t *testing.T) {
	off := runtime.Limits{}
	on := runtime.Limits{ContainerMinutesMonthly: 100}
	if got := (runtime.Limits{ContainerMinutesMonthly: 500}).CapTo(off); got.ContainerMinutesMonthly != 0 {
		t.Fatalf("partner off: %d", got.ContainerMinutesMonthly)
	}
	if got := (runtime.Limits{ContainerMinutesMonthly: 500}).CapTo(on); got.ContainerMinutesMonthly != 100 {
		t.Fatalf("partner 100: %d", got.ContainerMinutesMonthly)
	}
	if got := (runtime.Limits{}).CapTo(on); got.ContainerMinutesMonthly != 0 {
		t.Fatalf("sub-tenant off stays off: %d", got.ContainerMinutesMonthly)
	}
	if ex := (runtime.Limits{ContainerMinutesMonthly: 1}).Exceeds(off); len(ex) != 1 || ex[0] != "container_minutes_monthly" {
		t.Fatalf("exceeds %v", ex)
	}
	if v, err := runtime.ParseLimit("container_minutes_monthly", "6000"); err != nil || v != int64(6000) {
		t.Fatalf("parse %v %v", v, err)
	}
}

// Claims from the container queue hold a tenant to its container_concurrency.
func TestContainerConcurrencyCap(t *testing.T) {
	e := rt.New(t)
	enableContainers(t, e, 10)
	if err := e.Store.SetLimits(ctx, e.Tenant, map[string]any{"container_concurrency": 1, "worker_concurrency": 50}, "op"); err != nil {
		t.Fatal(err)
	}
	wf := e.Publish(t, wfDoc(containerStep(`,"class":"read"`), ""))
	for range 3 {
		e.Start(t, wf, map[string]any{"html": "x"})
	}
	var ready int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE queue = 'container'`).Scan(&ready); err != nil || ready != 3 {
		t.Fatalf("%d container tasks, %v", ready, err)
	}
	claim := func() int {
		var n int
		// As the worker claims: outside any tenant's scope.
		if err := e.DB.App.QueryRow(ctx, `SELECT count(*) FROM taskiem_claim_tasks('container', 'w1', 10, '60 seconds', 4)`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := claim(); n != 1 {
		t.Fatalf("first claim took %d, want 1", n)
	}
	if n := claim(); n != 0 {
		t.Fatalf("second claim took %d while one runs", n)
	}
}
