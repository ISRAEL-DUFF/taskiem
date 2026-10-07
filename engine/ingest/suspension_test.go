package ingest_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/runtime"
)

func (w *world) setStatus(t *testing.T, status string) {
	t.Helper()
	if _, err := w.DB.Admin.Exec(ctx, `UPDATE tenants SET status = $2 WHERE id = $1`, w.Tenant, status); err != nil {
		t.Fatal(err)
	}
}

func (w *world) statuses(t *testing.T, wf uuid.UUID) map[string]int {
	t.Helper()
	out := map[string]int{}
	rows, err := w.DB.Admin.Query(ctx, `SELECT status, count(*) FROM runs WHERE workflow_id = $1 GROUP BY status`, wf)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			t.Fatal(err)
		}
		out[s] = n
	}
	return out
}

// A suspended tenant does no new work: deliveries get 423 and are counted,
// schedules do not fire, queued runs are not admitted and the engine
// starts nothing. Resumed, its backlog runs, deliveries are taken again,
// and schedules continue from the resume without catching up.
func TestSuspendedTenantDoesNoNewWork(t *testing.T) {
	w := newWorld(t)
	w.limits(t, map[string]any{"ingest_rate": 0.001, "ingest_burst": 1})
	open := w.publish(t, openFlow)
	sched := w.publish(t, `{"schema":"wd/v1","id":"wf_daily","version":1,"name":"daily","trigger":{"type":"schedule","config":{"cron":"0 * * * *"}},
	  "steps":[{"id":"a","type":"transform","config":{"output":1}}]}`)
	// One run starts, the next is queued behind the soft rate.
	for i, body := range []string{`{"i":0}`, `{"i":1}`} {
		if st, out := w.post(t, "/open", []byte(body)); st != 202 || (out["queued"] == true) != (i == 1) {
			t.Fatalf("delivery %d: %d %v", i, st, out)
		}
	}
	w.setStatus(t, "suspended")
	w.limits(t, map[string]any{"ingest_rate": 100, "ingest_burst": 10})
	if _, err := w.DB.Admin.Exec(ctx, `UPDATE tenant_admission SET tokens = 5 WHERE tenant_id = $1`, w.Tenant); err != nil {
		t.Fatal(err)
	}

	// Deliveries: 423, recorded per day and kind, nothing started. This
	// edge still holds the tenant as active (its gate re-reads within a
	// minute): the engine refuses the start all the same.
	if st, out := w.post(t, "/open", []byte(`{"i":2}`)); st != 423 || out["code"] != "suspended" {
		t.Fatalf("webhook while suspended: %d %v", st, out)
	}
	// A fresh edge refuses at the gate, before anything else.
	fresh := &ingest.Handler{Store: w.Store, Secrets: w.Vault, Connections: w.Vault, Registry: w.Registry}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", fresh))
	edge := httptest.NewServer(mux)
	t.Cleanup(edge.Close)
	old := w.srv
	w.srv = edge
	if st, out := w.post(t, "/open", []byte(`{"i":2}`)); st != 423 || out["code"] != "suspended" {
		t.Fatalf("webhook at a fresh edge: %d %v", st, out)
	}
	if st, _ := w.post(t, "/connectors/paystack@1/transfer_event", []byte(`{}`)); st != 423 {
		t.Errorf("connector event while suspended: %d", st)
	}
	w.srv = old
	var hits int64
	if err := w.DB.Admin.QueryRow(ctx, `SELECT COALESCE(sum(hits), 0) FROM ingest_refusals WHERE tenant_id = $1 AND reason = 'suspended'`, w.Tenant).Scan(&hits); err != nil || hits != 3 {
		t.Errorf("refusals recorded: %d %v", hits, err)
	}
	// The engine refuses starts from any path.
	if _, _, err := w.Store.StartRun(ctx, runtime.StartRequest{TenantID: w.Tenant, WorkflowID: open, Version: 1, Environment: "prod",
		Trigger: map[string]any{"type": "manual"}}); !errors.Is(err, runtime.ErrTenantSuspended) {
		t.Errorf("start while suspended: %v", err)
	}
	// A schedule due now does not fire; the queued run waits.
	due := time.Now().Add(-time.Minute)
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE triggers SET next_fire_at = $2, lease_until = NULL WHERE workflow_id = $1`, sched, due)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c := &ingest.Cron{Store: w.Store}
	if n, err := c.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("schedule fired while suspended: %d %v", n, err)
	}
	w.Drain(t)
	if st := w.statuses(t, open); st["queued"] != 1 || st["completed"] != 1 {
		t.Fatalf("while suspended: %v", st)
	}

	// Resumed: the backlog runs, deliveries are taken, and the schedule
	// that fell due while suspended is skipped and moves on from now.
	w.setStatus(t, "active")
	w.Drain(t)
	if st := w.statuses(t, open); st["completed"] != 2 {
		t.Errorf("after resume: %v", st)
	}
	if st, _ := w.post(t, "/open", []byte(`{"i":3}`)); st != 202 {
		t.Errorf("webhook after resume: %d", st)
	}
	if n, err := c.Tick(ctx); err != nil || n != 0 {
		t.Errorf("a fire missed while suspended was caught up: %d %v", n, err)
	}
	if n := w.runCount(t, sched); n != 0 {
		t.Errorf("schedule runs: %d", n)
	}
	var next time.Time
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT next_fire_at FROM triggers WHERE workflow_id = $1`, sched).Scan(&next)
	}); err != nil || !next.After(time.Now()) {
		t.Errorf("next fire %v %v", next, err)
	}
	// Due again after the resume: it fires.
	if err := db.InTenantTx(ctx, w.DB.App, []uuid.UUID{w.Tenant}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE triggers SET next_fire_at = now(), lease_until = NULL WHERE workflow_id = $1`, sched)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := c.Tick(ctx); err != nil || n != 1 {
		t.Errorf("fire after resume: %d %v", n, err)
	}
}
