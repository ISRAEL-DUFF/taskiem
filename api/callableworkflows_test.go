package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A business's own workflow with outputs is callable (decision 0048): once
// published it is listed, another workflow uses it as a template step,
// the pins keep the version used, a new publication is offered as an
// update, and the run gives its outputs.
func TestCallableWorkflows(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	double := func(factor string) map[string]any {
		return map[string]any{"schema": "wd/v1", "id": "wf_double", "version": 1, "name": "Double it", "description": "Doubles an amount.",
			"trigger": map[string]any{"type": "manual"},
			"inputs":  map[string]any{"schema": map[string]any{"type": "object", "required": []string{"amount_kobo"}, "properties": map[string]any{"amount_kobo": map[string]any{"type": "integer"}}}},
			"steps":   []any{map[string]any{"id": "total", "type": "transform", "config": map[string]any{"output": map[string]any{"kobo": "=trigger.body.amount_kobo * " + factor}}}},
			"outputs": []any{map[string]any{"name": "kobo", "type": "integer", "title": "Total", "description": "The amount, doubled", "value": "=steps.total.output.kobo"}}}
	}
	// Nothing is callable before a publication.
	if got := owner.must(200, "GET", "/v1/workflows/callable", nil); len(got["workflows"].([]any)) != 0 {
		t.Fatalf("callable before any publication: %v", got)
	}
	a := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Double it", "definition": double("2")})
	if probs := a["problems"].([]any); len(probs) != 0 {
		t.Fatalf("a callable workflow: %v", probs)
	}
	aid := a["id"].(string)
	user := map[string]any{"schema": "wd/v1", "id": "wf_use", "version": 1, "name": "Use it", "trigger": map[string]any{"type": "manual"},
		"inputs": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"amount_kobo": map[string]any{"type": "integer"}}}},
		"steps": []any{
			map[string]any{"id": "sub", "type": "template", "workflow": aid, "input": map[string]any{"amount_kobo": "=trigger.body.amount_kobo"}},
			map[string]any{"id": "after", "type": "transform", "needs": []string{"sub"}, "config": map[string]any{"output": map[string]any{"kobo": "=steps.sub.output.kobo"}}}}}
	// Using it before it is published is a problem the draft carries.
	draft := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Too early", "definition": user})
	if probs := toJSON(draft["problems"]); !strings.Contains(probs, "no published version") {
		t.Fatalf("using an unpublished workflow: %v", probs)
	}
	owner.must(200, "POST", "/v1/workflows/"+aid+"/versions/1/publish", nil)

	// Listed with its interface.
	list := owner.must(200, "GET", "/v1/workflows/callable", nil)["workflows"].([]any)
	if len(list) != 1 {
		t.Fatalf("callable: %v", list)
	}
	c := list[0].(map[string]any)
	if c["id"] != aid || c["name"] != "Double it" || c["version"] != float64(1) || !strings.Contains(toJSON(c["inputs"]), "amount_kobo") ||
		!strings.Contains(toJSON(c["outputs"]), `"kobo"`) || c["effect"].(map[string]any)["class"] != "read" {
		t.Fatalf("callable: %v", c)
	}
	b := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Use it", "definition": user})
	if probs := b["problems"].([]any); len(probs) != 0 {
		t.Fatalf("the user: %v", probs)
	}
	bid := b["id"].(string)
	var pinned string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT template_id FROM workflow_template_pins WHERE workflow_id = $1 AND version = 1`, bid).Scan(&pinned); err != nil || pinned != aid {
		t.Fatalf("pinned %q: %v", pinned, err)
	}
	owner.must(200, "POST", "/v1/workflows/"+bid+"/versions/1/publish", nil)
	run := owner.must(201, "POST", "/v1/workflows/"+bid+"/runs", map[string]any{"input": map[string]any{"amount_kobo": 21}})["run_id"].(string)
	w.env.Drain(t)
	got := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if st := got["run"].(map[string]any)["status"]; st != "completed" || !strings.Contains(toJSON(got["events"]), `"kobo":42`) {
		t.Fatalf("run: %v\n%s", st, toJSON(got["events"]))
	}

	// A new publication of the callable workflow is offered to its user,
	// and the next version saved takes it; the published user keeps the
	// pinned one until then.
	owner.must(201, "POST", "/v1/workflows/"+aid+"/versions", map[string]any{"definition": double("3")})
	if ups := owner.must(200, "GET", "/v1/workflows/"+bid, nil)["template_updates"]; ups != nil {
		t.Fatalf("an unpublished version offered: %v", ups)
	}
	owner.must(200, "POST", "/v1/workflows/"+aid+"/versions/2/publish", nil)
	ups, _ := owner.must(200, "GET", "/v1/workflows/"+bid, nil)["template_updates"].([]any)
	if len(ups) != 1 || ups[0].(map[string]any)["template"] != aid || !strings.Contains(toJSON(ups[0]), "~ step total") {
		t.Fatalf("updates: %v", ups)
	}
	run = owner.must(201, "POST", "/v1/workflows/"+bid+"/runs", map[string]any{"input": map[string]any{"amount_kobo": 21}})["run_id"].(string)
	w.env.Drain(t)
	if got := owner.must(200, "GET", "/v1/runs/"+run, nil); !strings.Contains(toJSON(got["events"]), `"kobo":42`) {
		t.Fatalf("the published user changed under a new publication: %s", toJSON(got["events"]))
	}
	owner.must(201, "POST", "/v1/workflows/"+bid+"/versions", map[string]any{"definition": user})
	if ups := owner.must(200, "GET", "/v1/workflows/"+bid, nil)["template_updates"]; ups != nil {
		t.Errorf("after saving: %v", ups)
	}
	owner.must(200, "POST", "/v1/workflows/"+bid+"/versions/2/publish", nil)
	run = owner.must(201, "POST", "/v1/workflows/"+bid+"/runs", map[string]any{"input": map[string]any{"amount_kobo": 21}})["run_id"].(string)
	w.env.Drain(t)
	if got := owner.must(200, "GET", "/v1/runs/"+run, nil); !strings.Contains(toJSON(got["events"]), `"kobo":63`) {
		t.Fatalf("the new version does not use the new publication: %s", toJSON(got["events"]))
	}

	// A workflow without outputs is not callable, and a step naming it
	// says so.
	plain := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Plain", "definition": json.RawMessage(transformFlow)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+plain+"/versions/1/publish", nil)
	if list := owner.must(200, "GET", "/v1/workflows/callable", nil)["workflows"].([]any); len(list) != 1 {
		t.Errorf("callable: %v", list)
	}
	user["steps"].([]any)[0].(map[string]any)["workflow"] = plain
	if probs := toJSON(owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Uses plain", "definition": user})["problems"]); !strings.Contains(probs, "declares no outputs") {
		t.Errorf("using a workflow without outputs: %s", probs)
	}
	// A workflow may not use itself.
	self := double("2")
	self["steps"] = append(self["steps"].([]any), map[string]any{"id": "again", "type": "template", "workflow": aid, "needs": []string{"total"}, "input": map[string]any{"amount_kobo": 1}})
	if probs := toJSON(owner.must(201, "POST", "/v1/workflows/"+aid+"/versions", map[string]any{"definition": self})["problems"]); !strings.Contains(probs, "itself") && !strings.Contains(probs, "deep") {
		t.Errorf("a workflow using itself: %s", probs)
	}

	// Another business sees none of it.
	other := w.tenant(t, "Other", "owner@other.test")
	if list := other.must(200, "GET", "/v1/workflows/callable", nil)["workflows"].([]any); len(list) != 0 {
		t.Errorf("another business's callable workflows: %v", list)
	}
	user["steps"].([]any)[0].(map[string]any)["workflow"] = aid
	if probs := toJSON(other.must(201, "POST", "/v1/workflows", map[string]any{"name": "Theirs", "definition": user})["problems"]); !strings.Contains(probs, "no published version") {
		t.Errorf("another business using it: %s", probs)
	}

	// An embedded app's end user may not use the partner's customer's
	// workflows as steps.
	p := w.partner(t, "Wagely", "owner@wagely.test")
	sub := p.sub(t, "Customer", nil)
	app, _ := p.app(t, map[string]any{"name": "api", "headless": true, "allowed_connectors": []string{}, "allowed_templates": []string{},
		"end_user_permissions": []string{"workflow.read", "workflow.edit"}})
	u := p.endUser(t, w, app, sub, "cust-1", "", "workflow.read", "workflow.edit")
	out := u.call(403, "POST", "/workflows", map[string]any{"name": "x", "definition": user})
	if !strings.Contains(toJSON(out["not_allowed"]), "workflow:"+aid) {
		t.Errorf("embed refusal: %v", out)
	}
}
