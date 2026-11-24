package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// A tool's changes in plain words where people read them (build plan H6,
// g-ct-later): each version in the history says what changed from the one
// before ("It now also says 'diesel'"), and a version waiting for a second
// publisher says what publishing it changes from what is live
// ("Customers can now use it"), never an expression.
func TestToolChangesInPlainWords(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@toolwords.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "ben@toolwords.test", "password": "correct horse battery", "roles": []string{"builder"}})
	ben := w.login(t, "ben@toolwords.test", "correct horse battery")
	v1 := `{"schema":"wd/v1","id":"wf_fuel","version":1,"name":"Log fuel","trigger":{"type":"manual"},
		"inputs":{"schema":{"type":"object","required":["amount"],"properties":{"amount":{"type":"number"}}}},
		"outputs":[{"name":"amount","type":"number","title":"Amount","description":"Spent","value":"=steps.keep.output.amount","public":true}],
		"tool":{"name":"Log fuel","command":"fuel-log","about":"Record fuel","says":["fuel"],"answer":{"type":"text","text":"='Logged ' + string(outputs.amount)"}},
		"steps":[{"id":"keep","type":"transform","config":{"output":{"amount":"=trigger.body.amount"}}}]}`
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Log fuel", "definition": json.RawMessage(v1)})["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	v2 := strings.Replace(v1, `"says":["fuel"]`, `"says":["fuel","diesel"]`, 1)
	owner.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(v2)})
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/2/publish", nil)

	versions := owner.must(200, "GET", "/v1/workflows/"+wf, nil)["versions"].([]any)
	latest := toJSON(versions[0].(map[string]any)["changes"])
	if !strings.Contains(latest, `It now also says 'diesel'`) || strings.Contains(latest, "=") {
		t.Fatalf("version 2's changes: %s", latest)
	}
	if _, ok := versions[1].(map[string]any)["changes"]; ok {
		t.Errorf("the first version says changes: %v", versions[1])
	}
	// The changes report says them in the same words.
	if rep := toJSON(owner.must(200, "GET", "/v1/reports/changes", nil)["rows"]); !strings.Contains(rep, "It now also says 'diesel'") || strings.Contains(rep, "~ tool") {
		t.Errorf("the changes report: %s", rep)
	}

	// For customers, waiting for a second publisher: the review says so,
	// from the version live in prod.
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true})
	v3 := strings.Replace(v2, `"about":"Record fuel"`, `"about":"Record fuel","for":"customers"`, 1)
	ben.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": json.RawMessage(v3)})
	owner.must(202, "POST", "/v1/workflows/"+wf+"/versions/3/publish", nil)
	reqs := toJSON(ben.must(200, "GET", "/v1/workflows/"+wf, nil)["versions"])
	if !strings.Contains(reqs, "Customers can now use it") {
		t.Errorf("history: %s", reqs)
	}
	got := toJSON(owner.must(200, "GET", "/v1/publish-requests", nil)["requests"])
	if !strings.Contains(got, "Customers can now use it") || strings.Contains(got, "also says") {
		t.Fatalf("review: %s", got)
	}
}
