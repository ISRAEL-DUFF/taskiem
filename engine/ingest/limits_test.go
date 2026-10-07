package ingest_test

import (
	"bytes"
	"crypto/sha512"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/ingest"
)

const openFlow = `{"schema":"wd/v1","id":"wf_open","version":1,"name":"open","trigger":{"type":"webhook","config":{"path":"/open","auth":"none"}},
  "steps":[{"id":"a","type":"transform","config":{"output":1}}]}`

func (w *world) limits(t *testing.T, values map[string]any) {
	t.Helper()
	if err := w.Store.SetLimits(ctx, w.Tenant, values, "test"); err != nil {
		t.Fatal(err)
	}
}

// Above the soft rate deliveries are still accepted, their runs queued and
// started later at the tenant's rate; past the backlog cap they get 429.
func TestSoftIngestLimitQueuesRuns(t *testing.T) {
	w := newWorld(t)
	w.limits(t, map[string]any{"ingest_rate": 0.001, "ingest_burst": 1, "max_queued_runs": 2})
	w.publish(t, openFlow)
	var runs []string
	for i := 0; i < 3; i++ {
		st, out := w.post(t, "/open", []byte(`{"i":`+strconv.Itoa(i)+`}`))
		if st != 202 {
			t.Fatalf("delivery %d: %d %v", i, st, out)
		}
		if queued := out["queued"] == true; queued != (i > 0) {
			t.Errorf("delivery %d queued=%v", i, queued)
		}
		runs = append(runs, out["run_id"].(string))
	}
	st, out := w.post(t, "/open", []byte(`{"i":3}`))
	if st != 429 || out["code"] != "backlog_full" {
		t.Fatalf("beyond the backlog: %d %v", st, out)
	}
	// A provider retry of a queued delivery is a duplicate, not a new run.
	if st, out := w.post(t, "/open", []byte(`{"i":1}`)); st != 202 || out["duplicate"] != true || out["run_id"] != runs[1] {
		t.Errorf("retry while queued: %d %v", st, out)
	}
	// The operator raises the rate; the scheduler's tick admits the backlog.
	w.limits(t, map[string]any{"ingest_rate": 100, "ingest_burst": 10})
	if _, err := w.DB.Admin.Exec(ctx, `UPDATE tenant_admission SET tokens = 5 WHERE tenant_id = $1`, w.Tenant); err != nil {
		t.Fatal(err)
	}
	w.Drain(t)
	for i, id := range runs {
		var st string
		if err := w.DB.Admin.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, uuid.MustParse(id)).Scan(&st); err != nil || st != "completed" {
			t.Errorf("run %d: %s %v", i, st, err)
		}
	}
}

// A delivery above the soft rate still wakes waiting runs at once: only
// the runs it starts are queued.
func TestSignalsAreNotHeldBehindStarts(t *testing.T) {
	w := newWorld(t)
	w.limits(t, map[string]any{"ingest_rate": 0.001, "ingest_burst": 1})
	if _, err := w.Vault.CreateConnection(ctx, w.Tenant, "prod", "paystack", "main", "api_key", map[string]string{"secret_key": "sk_test_x"}, "test"); err != nil {
		t.Fatal(err)
	}
	w.publish(t, openFlow)
	settle := w.Publish(t, settleFlow)
	w.publish(t, onSuccessFlow)
	run := w.Start(t, settle, map[string]any{"body": map[string]any{"ref": "T-1"}})
	if st, _ := w.post(t, "/open", []byte(`{}`)); st != 202 { // uses the burst
		t.Fatalf("first delivery: %d", st)
	}
	body := []byte(`{"event":"transfer.success","data":{"reference":"T-1","amount":5000}}`)
	st, out := w.post(t, "/connectors/paystack@1/transfer_event", body, "x-paystack-signature", sign(sha512.New, "sk_test_x", body))
	if st != 202 || out["signalled"] != float64(1) {
		t.Fatalf("delivery: %d %v", st, out)
	}
	if s := w.Status(t, run); s != "completed" {
		t.Errorf("the waiting run was held back: %s", s)
	}
	started := out["runs"].([]any)
	var status string
	if err := w.DB.Admin.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, uuid.MustParse(started[0].(string))).Scan(&status); err != nil || status != "queued" {
		t.Errorf("the run it starts: %s %v", status, err)
	}
}

// Beyond the day's quota a delivery is refused with a clear code and
// Retry-After (nothing is recorded, so the provider's retry is not lost);
// a retry of a delivery accepted earlier is still recognised.
func TestQuotaRefusesDeliveries(t *testing.T) {
	w := newWorld(t)
	w.limits(t, map[string]any{"runs_per_day": 1})
	w.publish(t, openFlow)
	if st, _ := w.post(t, "/open", []byte(`{"n":1}`)); st != 202 {
		t.Fatalf("first: %d", st)
	}
	req, _ := http.NewRequest("POST", w.srv.URL+"/hooks/"+w.Tenant.String()+"/open", bytes.NewReader([]byte(`{"n":2}`)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Errorf("beyond the quota: %d Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if st, out := w.post(t, "/open", []byte(`{"n":2}`)); out["code"] != "quota_exceeded" || out["limit"] != "runs_per_day" {
		t.Errorf("body: %d %v", st, out)
	}
	if st, out := w.post(t, "/open", []byte(`{"n":1}`)); st != 202 || out["duplicate"] != true {
		t.Errorf("retry of an accepted delivery: %d %v", st, out)
	}
}

// Bodies over the tenant's payload limit are refused.
func TestPayloadLimit(t *testing.T) {
	w := newWorld(t)
	w.limits(t, map[string]any{"max_payload_bytes": 64})
	w.publish(t, openFlow)
	if st, _ := w.post(t, "/open", bytes.Repeat([]byte("x"), 100)); st != 413 {
		t.Errorf("large body: %d", st)
	}
	if st, _ := w.post(t, "/open", []byte(`{"ok":true}`)); st != 202 {
		t.Errorf("small body: %d", st)
	}
}

// A schedule fire beyond the quota is skipped, not retried every lease:
// the schedule moves on to its next time.
func TestScheduleFireBeyondQuotaIsSkipped(t *testing.T) {
	w := newWorld(t)
	w.limits(t, map[string]any{"runs_per_day": 1})
	wf := w.publish(t, `{"schema":"wd/v1","id":"wf_daily","version":1,"name":"daily","trigger":{"type":"schedule","config":{"cron":"0 9 * * *"}},
	  "steps":[{"id":"a","type":"transform","config":{"output":1}}]}`)
	other := w.Publish(t, `{"schema":"wd/v1","id":"wf_m","version":1,"name":"m","trigger":{"type":"manual"},"steps":[{"id":"a","type":"transform","config":{"output":1}}]}`)
	w.Start(t, other, map[string]any{}) // uses the day's quota
	due := time.Now().Add(-time.Minute).UTC()
	if _, err := w.DB.Admin.Exec(ctx, `UPDATE triggers SET next_fire_at = $2 WHERE workflow_id = $1`, wf, due); err != nil {
		t.Fatal(err)
	}
	c := &ingest.Cron{Store: w.Store}
	if n, err := c.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("tick: %d %v", n, err)
	}
	var next time.Time
	if err := w.DB.Admin.QueryRow(ctx, `SELECT next_fire_at FROM triggers WHERE workflow_id = $1`, wf).Scan(&next); err != nil || !next.After(time.Now()) {
		t.Errorf("schedule did not move on: %v %v", next, err)
	}
	if n := w.runCount(t, wf); n != 0 {
		t.Errorf("%d scheduled runs beyond the quota", n)
	}
}
