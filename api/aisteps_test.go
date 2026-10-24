package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ai"
)

// AI steps (decision 0050): saving checks the deployment's models and the
// business's switch, the status says what the provider keeps, publishing
// a step that lets personal data through is audited, and a run asks the
// model.
func TestAISteps(t *testing.T) {
	w, _ := aiWorld(t)
	w.srv.AI.Config = ai.Config{Provider: "fake", Model: "claude-opus-5-5", StepModels: []string{"claude-haiku-5-5"}, Retention: "Zero data retention under our agreement"}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	tenant := owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string)

	st := owner.must(200, "GET", "/v1/ai/status", nil)
	if st["steps_allowed"] != true || st["retention"] != "Zero data retention under our agreement" || !strings.Contains(toJSON(st["step_models"]), "claude-haiku-5-5") {
		t.Fatalf("status: %v", st)
	}
	flow := func(model string) map[string]any {
		cfg := map[string]any{"prompt": "Sort this message: urgent or not?",
			"output_schema": map[string]any{"type": "object", "required": []string{"urgent"}, "properties": map[string]any{"urgent": map[string]any{"type": "boolean"}}},
			"personal_data": []string{"phone"}}
		if model != "" {
			cfg["model"] = model
		}
		return map[string]any{"schema": "wd/v1", "id": "wf_sort", "version": 1, "name": "Sort", "trigger": map[string]any{"type": "manual"},
			"steps": []any{map[string]any{"id": "sort", "type": "ai", "config": cfg, "input": map[string]any{"text": "=trigger.body.text", "phone": "=trigger.body.phone"}}}}
	}
	if probs := toJSON(owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "x", "definition": flow("gpt-9")})["problems"]); !strings.Contains(probs, "not offered") {
		t.Errorf("an unoffered model: %s", probs)
	}
	// The business turns AI steps off; a request without the field keeps it.
	owner.must(200, "PUT", "/v1/governance", map[string]any{"ai_steps": false})
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": false})
	if g := owner.must(200, "GET", "/v1/governance", nil); g["ai_steps"] != false {
		t.Fatalf("governance: %v", g)
	}
	if st := owner.must(200, "GET", "/v1/ai/status", nil); st["steps_allowed"] != false {
		t.Errorf("status after turning off: %v", st)
	}
	if probs := toJSON(owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "x", "definition": flow("")})["problems"]); !strings.Contains(probs, "turned AI steps off") {
		t.Errorf("AI steps off: %s", probs)
	}
	owner.must(200, "PUT", "/v1/governance", map[string]any{"ai_steps": true})

	// Saved and published with the phone let through: audited.
	out := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Sort", "definition": flow("claude-haiku-5-5")})
	if probs := out["problems"].([]any); len(probs) != 0 {
		t.Fatalf("problems: %v", probs)
	}
	wf := out["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	if n := auditCount(t, w, tenant, "ai.personal_data.allow"); n != 1 {
		t.Errorf("audit entries: %d", n)
	}

	// A run asks the model.
	fake := &ai.Fake{Script: []ai.Response{{Text: `{"urgent":true}`, JSON: json.RawMessage(`{"urgent":true}`)}}}
	w.env.AI, w.env.AIModels = fake, []string{"claude-haiku-5-5"}
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"text": "Call me now", "phone": "+2348031234567"}})["run_id"].(string)
	w.env.Drain(t)
	got := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if st := got["run"].(map[string]any)["status"]; st != "completed" || !strings.Contains(toJSON(got["events"]), `"urgent":true`) || len(fake.Requests()) != 1 {
		t.Fatalf("run: %v\n%s", st, toJSON(got["events"]))
	}
	if !strings.Contains(fake.Requests()[0].Messages[0].Text, "+2348031234567") {
		t.Errorf("the phone, let through, did not reach the model: %s", fake.Requests()[0].Messages[0].Text)
	}
	// Its tokens count against the month's budget.
	if st := owner.must(200, "GET", "/v1/ai/status", nil); st["budget"].(map[string]any)["used_tokens"].(float64) == 0 {
		t.Errorf("budget use: %v", st["budget"])
	}
}

// A deployment without a model refuses AI steps when they are saved.
func TestAIStepsWithoutAModel(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	def := map[string]any{"schema": "wd/v1", "id": "wf_sort", "version": 1, "name": "Sort", "trigger": map[string]any{"type": "manual"},
		"steps": []any{map[string]any{"id": "sort", "type": "ai", "config": map[string]any{"prompt": "x", "output_schema": map[string]any{"type": "object"}}}}}
	if probs := toJSON(owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "x", "definition": def})["problems"]); !strings.Contains(probs, "this deployment has none") {
		t.Errorf("no model: %s", probs)
	}
	if st := owner.must(200, "GET", "/v1/ai/status", nil); st["enabled"] != false || st["steps_allowed"] != true {
		t.Errorf("status: %v", st)
	}
}
