package runtime_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// Dedicated worker pools (decision 0024): a tenant routed to a pool, its
// sub-tenants, and a plan's tenants are claimed only by that pool's
// workers; everyone else stays in the shared pool; workers of the previous
// release claim for the shared pool only; changes are audited.
func TestDedicatedWorkerPools(t *testing.T) {
	e := rt.New(t)
	shared := e.Tenant
	acme := e.AddTenant(t)
	sub := e.DB.SeedTenant(t, &acme).ID
	ent := e.AddTenant(t)
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO plans (id, name, tier, monthly_kobo, annual_kobo, updated_by) VALUES ('ent_test', 'Ent', 'enterprise', 0, 0, 'test')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.Admin.Exec(ctx, `INSERT INTO subscriptions (tenant_id, plan_id, status, period_start, period_end, updated_by)
		VALUES ($1, 'ent_test', 'active', now(), now() + interval '30 days', 'test')`, ent); err != nil {
		t.Fatal(err)
	}

	// Routing to a pool nobody serves is refused unless forced.
	if err := e.Store.AssignTenantPool(ctx, acme, "acme", "cli:ops", false); !errors.Is(err, runtime.ErrNoWorkers) {
		t.Fatalf("assign to an empty pool: %v", err)
	}
	if err := e.Store.AssignTenantPool(ctx, acme, "Not A Pool", "cli:ops", true); err == nil {
		t.Fatal("bad pool name accepted")
	}
	if err := e.Store.AssignPlanPool(ctx, "no_such_plan", "ent", "cli:ops", true); err == nil {
		t.Fatal("unknown plan accepted")
	}
	acmeWorker := e.Worker("w-acme")
	acmeWorker.Pool = "acme"
	if _, err := e.DB.App.Exec(ctx, `SELECT taskiem_worker_seen('w-acme', 'connector', 'acme')`); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.AssignTenantPool(ctx, acme, "acme", "cli:ops", false); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.AssignPlanPool(ctx, "ent_test", "ent", "cli:ops", true); err != nil {
		t.Fatal(err)
	}
	for tenant, want := range map[uuid.UUID]string{shared: "shared", acme: "acme", sub: "acme", ent: "ent"} {
		if got, err := e.Store.TenantPool(ctx, tenant); err != nil || got != want {
			t.Errorf("tenant pool %q (%v), want %q", got, err, want)
		}
	}

	runs := map[uuid.UUID]runtime.RunRef{}
	for _, tn := range []uuid.UUID{shared, acme, sub, ent} {
		wf := e.PublishIn(t, tn, wfDoc(payOne, ""))
		st, err := e.Store.Start(ctx, runtime.StartRequest{TenantID: tn, WorkflowID: wf, Version: 1, Environment: "prod",
			Trigger: map[string]any{"amount": 100}, Env: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		runs[tn] = st.Ref
	}
	depth, err := e.Store.PoolStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ready := map[string]int64{}
	for _, d := range depth {
		ready[d.Pool+"/"+d.Queue] = d.Ready
	}
	if ready["shared/connector"] != 1 || ready["acme/connector"] != 2 || ready["ent/connector"] != 1 {
		t.Errorf("pool stats %v", ready)
	}

	// A worker of the previous release (five-argument claim) sees the
	// shared pool only.
	var old int
	if err := e.DB.App.QueryRow(ctx, `SELECT count(*) FROM taskiem_claim_tasks('connector', 'old', 10, '60 seconds', 0)`).Scan(&old); err != nil || old != 1 {
		t.Fatalf("old claim took %d (%v), want the shared tenant's one", old, err)
	}
	if _, err := e.DB.Admin.Exec(ctx, `UPDATE tasks SET lease_owner = NULL, lease_until = NULL WHERE lease_owner = 'old'`); err != nil {
		t.Fatal(err)
	}

	sharedWorker := e.Worker("w-shared")
	if n, err := sharedWorker.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("shared worker ran %d (%v), want 1", n, err)
	}
	if n, err := acmeWorker.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("acme worker ran %d (%v), want 2 (acme and its sub-tenant)", n, err)
	}
	entWorker := e.Worker("w-ent")
	entWorker.Pool = "ent"
	if n, err := entWorker.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("ent worker ran %d (%v), want 1", n, err)
	}
	for tn, ref := range runs {
		if st, err := e.Store.RunStatus(ctx, runtime.RunRef{ID: ref.ID, TenantID: tn}); err != nil || st != "completed" {
			t.Errorf("tenant %s run: %s %v", tn, st, err)
		}
	}

	// Removing the routing puts acme back in the shared pool; the change
	// log and acme's audit chain both have it.
	if err := e.Store.AssignTenantPool(ctx, acme, "", "cli:ops", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.Store.TenantPool(ctx, acme); got != "shared" {
		t.Errorf("after unassign: %q", got)
	}
	var audited, logged int
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action IN ('worker_pool.assign', 'worker_pool.unassign') AND actor_type = 'platform_admin' AND actor_id = 'cli:ops'`, acme).Scan(&audited); err != nil || audited != 2 {
		t.Errorf("audited %d (%v), want 2", audited, err)
	}
	if err := e.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM worker_pool_changes`).Scan(&logged); err != nil || logged != 3 {
		t.Errorf("change log %d (%v), want 3", logged, err)
	}
	routes, err := e.Store.PoolAssignments(ctx)
	if err != nil || len(routes) != 1 || routes[0].Plan != "ent_test" || routes[0].Pool != "ent" {
		t.Errorf("routes %+v (%v)", routes, err)
	}
	live, err := e.Store.LiveWorkers(ctx)
	if err != nil || len(live) != 1 || live[0].Pool != "acme" {
		t.Errorf("live %+v (%v)", live, err)
	}
}

// Within a pool, one tenant's flood does not starve another: claims take
// round-robin across tenants.
func TestPoolClaimsFairAcrossTenants(t *testing.T) {
	e := rt.New(t)
	other := e.AddTenant(t)
	wfA := e.Publish(t, wfDoc(payOne, ""))
	wfB := e.PublishIn(t, other, wfDoc(payOne, ""))
	for i := 0; i < 20; i++ {
		if _, err := e.Store.Start(ctx, runtime.StartRequest{TenantID: e.Tenant, WorkflowID: wfA, Version: 1, Environment: "prod",
			Trigger: map[string]any{"amount": 1}, Env: map[string]any{}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Store.Start(ctx, runtime.StartRequest{TenantID: other, WorkflowID: wfB, Version: 1, Environment: "prod",
		Trigger: map[string]any{"amount": 1}, Env: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	var tenants []uuid.UUID
	rows, err := e.DB.App.Query(ctx, `SELECT tenant_id FROM taskiem_claim_tasks('connector', 'w', 2, '60 seconds', 0, 'shared')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		tenants = append(tenants, id)
	}
	rows.Close()
	if len(tenants) != 2 || tenants[0] == tenants[1] {
		t.Errorf("two claims went to %v, want one per tenant", tenants)
	}
}
