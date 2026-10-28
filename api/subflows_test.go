package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai/builder"
)

// A subflow step (decision 0049) is checked against the business's
// workflows when a version is saved, runs the child as its own run, and
// the run page links the two.
func TestSubflowSteps(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	child := map[string]any{"schema": "wd/v1", "id": "wf_double", "version": 1, "name": "Double it",
		"trigger": map[string]any{"type": "subflow"},
		"inputs":  map[string]any{"schema": map[string]any{"type": "object", "required": []string{"amount_kobo"}, "additionalProperties": false, "properties": map[string]any{"amount_kobo": map[string]any{"type": "integer"}}}},
		"steps":   []any{map[string]any{"id": "total", "type": "transform", "config": map[string]any{"output": map[string]any{"kobo": "=trigger.body.amount_kobo * 2"}}}},
		"outputs": []any{map[string]any{"name": "kobo", "type": "integer", "title": "Total", "description": "Twice", "value": "=steps.total.output.kobo"}}}
	cid := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Double it", "definition": child})["id"].(string)
	parent := func(cfg map[string]any, input map[string]any) map[string]any {
		return map[string]any{"schema": "wd/v1", "id": "wf_p", "version": 1, "name": "Parent", "trigger": map[string]any{"type": "manual"},
			"steps": []any{map[string]any{"id": "sub", "type": "subflow", "config": cfg, "input": input},
				map[string]any{"id": "after", "type": "transform", "needs": []string{"sub"}, "config": map[string]any{"output": "=steps.sub.output.outputs.kobo"}}}}
	}
	// What the save-time checks refuse, in order: the child is published
	// before the last case.
	for _, c := range []struct {
		name  string
		cfg   map[string]any
		input map[string]any
		want  string
	}{
		{"missing", map[string]any{"workflow": "00000000-0000-4000-8000-000000000000"}, map[string]any{"amount_kobo": 1}, "there is no workflow"},
		{"unpublished", map[string]any{"workflow": cid}, map[string]any{"amount_kobo": 1}, "no published version"},
		{"no version", map[string]any{"workflow": cid, "version": 7}, map[string]any{"amount_kobo": 1}, "no version 7"},
		{"input", map[string]any{"workflow": cid}, map[string]any{"amount": 1}, "needs amount_kobo"},
	} {
		if c.name == "input" {
			owner.must(200, "POST", "/v1/workflows/"+cid+"/versions/1/publish", nil)
		}
		out := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "x " + c.name, "definition": parent(c.cfg, c.input)})
		if probs := toJSON(out["problems"]); !strings.Contains(probs, c.want) {
			t.Errorf("%s: %s, want %q", c.name, probs, c.want)
		}
	}
	// A workflow that starts on its own cannot be run by another.
	sched := map[string]any{"schema": "wd/v1", "id": "wf_s", "version": 1, "name": "Daily", "trigger": map[string]any{"type": "schedule", "config": map[string]any{"cron": "0 9 * * *"}},
		"steps": []any{map[string]any{"id": "t", "type": "transform", "config": map[string]any{"output": 1}}}}
	sid := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Daily", "definition": sched})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+sid+"/versions/1/publish", nil)
	if probs := toJSON(owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "x", "definition": parent(map[string]any{"workflow": sid}, map[string]any{})})["problems"]); !strings.Contains(probs, "starts on its own") {
		t.Errorf("a scheduled workflow as a subflow: %s", probs)
	}

	// The good one runs the child as its own run, and the pages link them.
	good := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Parent", "definition": parent(map[string]any{"workflow": cid}, map[string]any{"amount_kobo": "=trigger.body.amount_kobo"})})
	if probs := good["problems"].([]any); len(probs) != 0 {
		t.Fatalf("the parent: %v", probs)
	}
	pid := good["id"].(string)
	// A workflow may not run itself.
	self := parent(map[string]any{"workflow": pid}, map[string]any{})
	if probs := toJSON(owner.must(201, "POST", "/v1/workflows/"+pid+"/versions", map[string]any{"definition": self})["problems"]); !strings.Contains(probs, "run itself") {
		t.Errorf("a workflow running itself: %s", probs)
	}
	owner.must(200, "POST", "/v1/workflows/"+pid+"/versions/1/publish", nil)
	run := owner.must(201, "POST", "/v1/workflows/"+pid+"/runs", map[string]any{"input": map[string]any{"amount_kobo": 21}})["run_id"].(string)
	w.env.Drain(t)
	got := owner.must(200, "GET", "/v1/runs/"+run, nil)
	forks := got["forks"].(map[string]any)
	subs, _ := forks["subflows"].([]any)
	if st := got["run"].(map[string]any)["status"]; st != "completed" || len(subs) != 1 || !strings.Contains(toJSON(got["events"]), `"output":42`) {
		t.Fatalf("the parent run: %v %v\n%s", st, forks, toJSON(got["events"]))
	}
	childRun := owner.must(200, "GET", "/v1/runs/"+subs[0].(string), nil)
	cf := childRun["forks"].(map[string]any)
	if cf["parent_run_id"] != run || cf["parent_step"] != "sub" || childRun["run"].(map[string]any)["status"] != "completed" || !strings.HasPrefix(childRun["run"].(map[string]any)["started_by"].(string), "subflow:") {
		t.Errorf("the child run: %v %v", cf, childRun["run"])
	}
	// The child's run appears in its workflow's runs.
	if runs := owner.must(200, "GET", "/v1/runs?workflow="+cid, nil)["runs"].([]any); len(runs) != 1 {
		t.Errorf("the child's runs: %v", runs)
	}

}

// The builder is told which workflows a subflow step may run: the
// business's published ones started by hand or by another run.
func TestAIBuildListsStartableWorkflows(t *testing.T) {
	w, f := aiWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	flow := func(id, name, trigger string) map[string]any {
		return map[string]any{"schema": "wd/v1", "id": id, "version": 1, "name": name, "trigger": map[string]any{"type": trigger},
			"inputs": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"amount_kobo": map[string]any{"type": "integer"}}}},
			"steps":  []any{map[string]any{"id": "total", "type": "transform", "config": map[string]any{"output": map[string]any{"kobo": "=trigger.body.amount_kobo"}}}}}
	}
	for _, c := range []struct{ id, name, trigger string }{{"wf_child", "Child", "subflow"}, {"wf_byHand", "By hand", "manual"}, {"wf_draft", "Draft only", "manual"}} {
		id := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": c.name, "definition": flow(c.id, c.name, c.trigger)})["id"].(string)
		if c.id != "wf_draft" {
			owner.must(200, "POST", "/v1/workflows/"+id+"/versions/1/publish", nil)
		}
	}
	build(t, w, owner, map[string]any{"goal": "run my Child workflow for each order"})
	ctxDoc, _ := builder.ContextFrom(f.Requests()[0].Messages[0].Text)
	raw := toJSON(ctxDoc["startable_workflows"])
	if !strings.Contains(raw, `"Child"`) || !strings.Contains(raw, `"By hand"`) || !strings.Contains(raw, "amount_kobo") || strings.Contains(raw, "Draft only") {
		t.Errorf("startable workflows: %s", raw)
	}
}
