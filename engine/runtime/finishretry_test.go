package runtime_test

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/israel-duff/taskiem/engine/history"
	"github.com/israel-duff/taskiem/engine/runtime"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// Recording an outcome is retried when the database goes away at its
// COMMIT, so it runs twice. A capability step's output is projected from
// the rail's answer once all the same: the second pass starts from the
// rail's answer again, not from the first pass's projection.
func TestFinishIsRecordedTheSameWhenItRunsTwice(t *testing.T) {
	e := rt.New(t)
	r := newRail(t, e, "testrail", "Test Rail")
	connect(t, e, "rail", "testrail")
	wf := e.Publish(t, payrollWD)
	ref := e.Start(t, wf, map[string]any{"people": people["people"].([]any)[:1]})

	px := e.DB.NewProxy(t)
	w := e.Worker("w-twice")
	w.Store = &runtime.Store{Pool: px.Pool(t, e.DB, "taskiem_app", 4, nil), Registry: e.Registry, PII: e.Vault}
	var cuts atomic.Int32
	w.Hooks = &runtime.Hooks{AfterCall: func(inst string, _ int) error {
		if strings.HasSuffix(inst, ".pay") && cuts.Add(1) == 1 {
			px.CutAtCommit("before") // the outcome's COMMIT never arrives
		}
		return nil
	}}
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cuts.Load() != 1 {
		t.Fatalf("the payout was called %d times", cuts.Load())
	}
	r.mu.Lock()
	for k, n := range r.paid {
		if n != 1 {
			t.Errorf("key %s paid %d times", k, n)
		}
	}
	r.mu.Unlock()
	seen := false
	for _, ev := range events(t, e, ref) {
		if !strings.HasSuffix(ev.StepID, ".pay") || (ev.Type != history.StepCompleted && ev.Type != history.StepFailed) {
			continue
		}
		seen = true
		var p struct {
			Output         map[string]any `json:"output"`
			ProviderOutput map[string]any `json:"provider_output"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		if ev.Type != history.StepCompleted || p.Output["status"] != "pending" || p.Output["fee_kobo"] != float64(1075) ||
			!strings.HasPrefix(str(p.Output["payout_id"]), "po_") || p.ProviderOutput["state"] != "queued" {
			t.Errorf("%s %s: %s", ev.Type, ev.StepID, ev.Payload)
		}
	}
	if !seen {
		t.Fatalf("no outcome for the payout: %s", types(events(t, e, ref)))
	}
}
