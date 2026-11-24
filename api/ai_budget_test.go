package api_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
)

// Builds started at once see each other's model calls in flight (finding
// F49): each reserves what its call may use before calling, so a budget
// one call can spend is not spent by five builds that all checked it
// before any had recorded its tokens.
func TestAIBudgetCountsBuildsInFlight(t *testing.T) {
	w, _ := aiWorld(t)
	release := make(chan struct{})
	var inFlight, most, calls atomic.Int64
	env := aiEnvelope(t, builder.ExampleWD)
	w.srv.AI = &api.AISettings{Provider: &ai.Fake{Models: "claude-opus-5-5", Respond: func(ai.Request) (*ai.Response, error) {
		calls.Add(1)
		n := inFlight.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		<-release
		inFlight.Add(-1)
		r := env
		r.Usage = ai.Usage{InputTokens: 40000, OutputTokens: 20000}
		return &r, nil
	}}}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	tenant := uuid.MustParse(owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string))
	// Less than one build's call may use (its prompt and longest answer).
	if err := w.env.Store.SetLimits(context.Background(), tenant, map[string]any{"ai_monthly_tokens": int64(30000)}, "operator"); err != nil {
		t.Fatal(err)
	}
	var ids []string
	refused := 0
	for range 5 {
		st, out := owner.do("POST", "/v1/ai/build", map[string]any{"goal": "pay approved loans with paystack"})
		switch {
		case st == 202:
			ids = append(ids, out["id"].(string))
		case st == 429 && out["code"] == "ai_budget_exhausted":
			refused++
		default:
			t.Fatalf("build: %d %v", st, out)
		}
	}
	// Wait until every build but the one calling has ended.
	for deadline := time.Now().Add(20 * time.Second); ; {
		ended := 0
		for _, id := range ids {
			if b := owner.must(200, "GET", "/v1/ai/builds/"+id, nil); b["status"] != "running" {
				ended++
			}
		}
		if inFlight.Load() == 1 && ended+refused == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d calls in flight, %d builds ended, %d refused", inFlight.Load(), ended, refused)
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(release)
	w.srv.WaitBackground()
	if calls.Load() != 1 || most.Load() != 1 {
		t.Fatalf("%d model calls, %d at once; want 1", calls.Load(), most.Load())
	}
	proposed := 0
	for _, id := range ids {
		if b := owner.must(200, "GET", "/v1/ai/builds/"+id, nil); b["status"] == "proposed" {
			proposed++
		}
	}
	lim := owner.must(200, "GET", "/v1/limits", nil)
	if used := lim["usage"].(map[string]any)["ai_tokens_this_month"]; proposed != 1 || used != float64(60000) {
		t.Fatalf("%d builds proposed, %v tokens used", proposed, used)
	}
	// The reservation went with the call: the budget shows what was spent.
	if b := owner.must(200, "GET", "/v1/ai/status", nil)["budget"].(map[string]any); b["used_tokens"] != float64(60000) {
		t.Fatalf("status: %v", b)
	}
}
