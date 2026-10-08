package api_test

import (
	"context"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/telemetry"
)

func replicaReads() float64 {
	var m dto.Metric
	_ = telemetry.DBReads.WithLabelValues("replica").Write(&m)
	return m.GetCounter().GetValue()
}

// With a read replica (decision 0024), the run list and the dashboard read
// from it, under each tenant's row-level security, while starting,
// revealing (audited), reading and cancelling runs never touch it: the
// stand-in replica is read-only, so a write sent there would fail the
// request.
func TestReadReplicaNeverWrites(t *testing.T) {
	w := newWorld(t)
	px := w.env.DB.NewProxy(t)
	pool := px.Pool(t, w.env.DB, "taskiem_app", 4, map[string]string{"application_name": "replica", "default_transaction_read_only": "on"})
	r := &db.Replica{Pool: pool, Primary: w.env.DB.App, MaxLag: 5 * time.Second}
	if err := r.Check(context.Background()); err != nil || !r.InUse() {
		t.Fatalf("replica: %v", err)
	}
	w.env.Store.Read = r
	defer func() { w.env.Store.Read = nil }()

	owner := w.tenant(t, "Acme", "owner@acme.test")
	other := w.tenant(t, "Other", "owner@other.test")
	wf := publishFlow(t, owner, loanFlow)
	before := replicaReads()

	in := map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 5000}}
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", in, "Idempotency-Key", "loan-1")["run_id"].(string)
	if runs := owner.must(200, "GET", "/v1/runs?workflow="+wf, nil)["runs"].([]any); len(runs) != 1 {
		t.Fatalf("runs from the replica: %v", runs)
	}
	if runs := other.must(200, "GET", "/v1/runs", nil)["runs"].([]any); len(runs) != 0 {
		t.Fatalf("another tenant sees %d runs through the replica", len(runs))
	}
	if d := owner.must(200, "GET", "/v1/dashboard", nil); d["runs"].(map[string]any)["total"] != float64(1) {
		t.Errorf("dashboard from the replica: %v", d["runs"])
	}
	owner.must(200, "GET", "/v1/runs/"+run+"?reveal=true", nil)
	owner.must(200, "POST", "/v1/runs/"+run+"/cancel", nil)
	if st := owner.must(200, "GET", "/v1/runs/"+run, nil)["run"].(map[string]any)["status"]; st != "cancelled" {
		t.Errorf("after cancel: %v", st)
	}
	if got := replicaReads() - before; got != 3 {
		t.Errorf("%v reads went to the replica, want 3 (two lists, one dashboard)", got)
	}
	if !r.InUse() {
		t.Error("the replica was set aside: something failed on it")
	}
}
