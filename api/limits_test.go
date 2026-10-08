package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// Any member sees the tenant's limits and usage; nobody in the tenant can
// change them through the API; starts and creations beyond them get 429
// with a code.
func TestLimitsReadOnlyAndEnforced(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	viewer := addMember(t, w, owner, "vic", "viewer")
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM tenants WHERE name = 'Acme'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if err := w.env.Store.SetLimits(context.Background(), tenant, map[string]any{"runs_per_month": 1, "max_workflows": 1, "max_secrets": 1}, "operator"); err != nil {
		t.Fatal(err)
	}

	v := viewer.must(200, "GET", "/v1/limits", nil)
	limits := v["limits"].(map[string]any)
	if limits["runs_per_month"] != float64(1) || limits["ingest_rate"] == nil || v["overrides"].(map[string]any)["max_workflows"] != float64(1) {
		t.Fatalf("limits: %v", v)
	}
	if v["help"].(map[string]any)["max_queued_runs"] == "" {
		t.Error("no help for limits")
	}
	for _, m := range []string{"PUT", "POST", "PATCH", "DELETE"} {
		if st, _ := owner.do(m, "/v1/limits", map[string]any{"limits": map[string]any{"runs_per_month": 1000000}}); st != 405 {
			t.Errorf("%s /v1/limits: %d", m, st)
		}
	}

	wf := publishFlow(t, owner, approvalFlow)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	st, out := owner.do("POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{}})
	if st != 429 || out["code"] != "quota_exceeded" || out["limit"] != "runs_per_month" {
		t.Errorf("start beyond the quota: %d %v", st, out)
	}
	st, out = owner.do("POST", "/v1/workflows", map[string]any{"name": "two", "definition": json.RawMessage(approvalFlow)})
	if st != 429 || out["code"] != "limit_exceeded" || out["limit"] != "max_workflows" {
		t.Errorf("second workflow: %d %v", st, out)
	}
	owner.must(204, "PUT", "/v1/secrets/prod/one", map[string]any{"value": "x"})
	owner.must(204, "PUT", "/v1/secrets/prod/one", map[string]any{"value": "y"}) // replacing is not adding
	if st, out := owner.do("PUT", "/v1/secrets/prod/two", map[string]any{"value": "x"}); st != 429 || out["limit"] != "max_secrets" {
		t.Errorf("second secret: %d %v", st, out)
	}

	u := viewer.must(200, "GET", "/v1/limits", nil)["usage"].(map[string]any)
	if u["runs_this_month"] != float64(1) || u["workflows"] != float64(1) || u["secrets"] != float64(1) {
		t.Errorf("usage: %v", u)
	}
	if hits := viewer.must(200, "GET", "/v1/limits", nil)["recent_hits"].([]any); len(hits) < 3 {
		t.Errorf("recent hits: %v", hits)
	}
}
