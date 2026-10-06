package api_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestDashboard(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	owner := w.tenant(t, "Acme", "owner@acme.test")
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'Acme'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	wf := publishFlow(t, owner, loanFlow)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := w.env.DB.Admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Three completed runs (10s, 20s, 30s), one failed after two failed
	// attempts at a Paystack transfer, one in dev, one needing reconciliation.
	for _, secs := range []int{10, 20, 30} {
		exec(`INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at, ended_at)
			VALUES ($1, $2, $3, 1, 'prod', 'completed', now() - interval '1 hour', now() - interval '1 hour' + make_interval(secs => $4))`, uuid.Must(uuid.NewV7()), tenant, wf, secs)
	}
	failed := uuid.Must(uuid.NewV7())
	exec(`INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at, ended_at, last_seq)
		VALUES ($1, $2, $3, 1, 'prod', 'failed', date_trunc('second', now()) - interval '1 hour', now(), 4)`, failed, tenant, wf)
	for a := 1; a <= 2; a++ {
		exec(`INSERT INTO run_events (run_id, seq, run_started_at, tenant_id, type, step_id, attempt, payload)
			SELECT $1, $2, started_at, $3, 'StepScheduled', 'pay', $4, '{"kind":"connector","connector":"paystack@1","action":"transfer"}' FROM runs WHERE id = $1`, failed, 2*a-1, tenant, a)
		exec(`INSERT INTO run_events (run_id, seq, run_started_at, tenant_id, type, step_id, attempt, payload)
			SELECT $1, $2, started_at, $3, 'StepFailed', 'pay', $4, '{"error":{"kind":"retryable","message":"503","next":"retry"}}' FROM runs WHERE id = $1`, failed, 2*a, tenant, a)
	}
	exec(`INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at, ended_at) VALUES ($1, $2, $3, 1, 'dev', 'failed', now(), now())`, uuid.Must(uuid.NewV7()), tenant, wf)
	exec(`INSERT INTO runs (id, tenant_id, workflow_id, version, environment, status, started_at) VALUES ($1, $2, $3, 1, 'prod', 'needs_reconciliation', now())`, uuid.Must(uuid.NewV7()), tenant, wf)

	d := owner.must(200, "GET", "/v1/dashboard?environment=prod&days=7", nil)
	runs := d["runs"].(map[string]any)
	if runs["total"] != float64(5) || runs["completed"] != float64(3) || runs["failed"] != float64(1) {
		t.Fatalf("runs: %v", runs)
	}
	if d["success_rate"] != 0.75 || d["needs_reconciliation"] != float64(1) {
		t.Errorf("rate %v, reconciliation %v", d["success_rate"], d["needs_reconciliation"])
	}
	if p := d["duration_seconds"].(map[string]any); p["p50"] != float64(20) || p["p95"].(float64) < 28 {
		t.Errorf("durations %v", p)
	}
	if got := toJSON(d["failures_by_step"]); got != `[{"connector":"paystack@1","count":2,"step":"pay","workflow":"loan"}]` {
		t.Errorf("by step: %s", got)
	}
	if got := toJSON(d["failures_by_connector"]); got != `[{"connector":"paystack@1","count":2,"kind":"retryable"}]` {
		t.Errorf("by connector: %s", got)
	}
	if n := len(d["daily"].([]any)); n < 7 || n > 9 {
		t.Errorf("%d days", n)
	}
	all := owner.must(200, "GET", "/v1/dashboard", nil)
	if all["runs"].(map[string]any)["failed"] != float64(2) || len(all["workflows"].([]any)) != 1 {
		t.Errorf("all environments: %v", all)
	}
	owner.must(400, "GET", "/v1/dashboard?tz=Mars/Base", nil)
}
