package api_test

import (
	"encoding/json"
	"testing"
)

// The builder's Test runs a draft with sample data: nothing is stored or
// sent; each step's state and output come back, a choice takes the path
// the trigger given picks, and an approval is approved.
func TestTryDraft(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Try Stores", "owner@try.test")
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Try", "definition": json.RawMessage(`{"schema":"wd/v1","id":"wf_try","version":1,"name":"Try","trigger":{"type":"manual"},"steps":[]}`)})["id"].(string)
	draft := json.RawMessage(`{"schema":"wd/v1","id":"wf_try","version":1,"name":"Try","trigger":{"type":"manual"},
	 "inputs":{"schema":{"type":"object","properties":{"total":{"type":"integer"}}}},
	 "steps":[
	  {"id":"ok","type":"approval","config":{"role":"owner"}},
	  {"id":"big","type":"branch","needs":["ok"],"config":{"paths":[{"name":"yes","when":"=trigger.body.total > 100","steps":[
	    {"id":"thank","type":"transform","config":{"output":{"said":"=string(trigger.body.total)"}}}]}],
	   "default":{"steps":[{"id":"small","type":"transform","config":{"output":{"said":"small"}}}]}}}]}`)
	out := owner.must(200, "POST", "/v1/workflows/"+wf+"/try", map[string]any{"definition": draft, "trigger": map[string]any{"body": map[string]any{"total": 500}}})
	if out["status"] != "completed" {
		t.Fatalf("status: %v", out)
	}
	steps := out["steps"].(map[string]any)
	if steps["ok"] != "completed" || steps["thank"] != "completed" || steps["small"] == "completed" {
		t.Errorf("steps: %v", steps)
	}
	if said := out["outputs"].(map[string]any)["thank"].(map[string]any)["said"]; said != "500" {
		t.Errorf("the yes path's output: %v", said)
	}
	// Nothing was stored: the workflow has no runs.
	if runs := owner.must(200, "GET", "/v1/runs?workflow="+wf, nil)["runs"].([]any); len(runs) != 0 {
		t.Errorf("a test left runs: %v", runs)
	}
	owner.must(422, "POST", "/v1/workflows/"+wf+"/try", map[string]any{"definition": json.RawMessage(`{"schema":"wd/v1","id":"wf_try"}`)})
}
