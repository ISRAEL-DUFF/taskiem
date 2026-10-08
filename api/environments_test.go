package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func hookFlow(factor string) json.RawMessage {
	return json.RawMessage(`{"schema":"wd/v1","id":"wf_hook","version":1,"name":"hook","trigger":{"type":"webhook","config":{"path":"/payouts","auth":"none"}},
	  "steps":[{"id":"x","type":"transform","config":{"output":"=` + factor + `"}}]}`)
}

// deployedIn maps environment to version from a workflow's deployments.
func deployedIn(t *testing.T, c *client, wf string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, d := range c.must(200, "GET", "/v1/workflows/"+wf, nil)["deployments"].([]any) {
		d := d.(map[string]any)
		out[d["environment"].(string)] = int(d["version"].(float64))
	}
	return out
}

// triggerVersions maps environment to the version its webhook starts.
func triggerVersions(t *testing.T, c *client, wf string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, tr := range c.must(200, "GET", "/v1/workflows/"+wf+"/triggers", nil)["triggers"].([]any) {
		tr := tr.(map[string]any)
		out[tr["environment"].(string)] = int(tr["version"].(float64))
	}
	return out
}

func TestStagingAndPromotion(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")

	// Staging joins; prod is gated on it.
	owner.must(201, "POST", "/v1/environments", map[string]any{"name": "staging"})
	owner.must(200, "PUT", "/v1/environments/prod", map[string]any{"promotion_from": "staging"})
	owner.must(400, "PUT", "/v1/environments/staging", map[string]any{"promotion_from": "prod"}) // a circle
	owner.must(400, "POST", "/v1/environments", map[string]any{"name": "Bad Name"})
	owner.must(409, "POST", "/v1/environments", map[string]any{"name": "staging"})

	created := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "hook", "definition": hookFlow("1")})
	wf := created["id"].(string)
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	if got := deployedIn(t, owner, wf); got["dev"] != 1 || got["staging"] != 1 || got["prod"] != 0 {
		t.Fatalf("after publishing: %v", got)
	}
	// Prod runs nothing until promoted, by hand or by webhook.
	owner.must(409, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"environment": "prod"})
	owner.must(409, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"environment": "prod", "version": 1})
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"environment": "staging"})
	if got := triggerVersions(t, owner, wf); got["prod"] != 0 || got["staging"] != 1 {
		t.Errorf("webhooks before promotion: %v", got)
	}

	owner.must(400, "POST", "/v1/workflows/"+wf+"/promote", map[string]any{"from": "dev", "to": "prod"})
	owner.must(409, "POST", "/v1/workflows/"+wf+"/promote", map[string]any{"from": "staging", "to": "prod", "version": 2})
	owner.must(200, "POST", "/v1/workflows/"+wf+"/promote", map[string]any{"from": "staging", "to": "prod"})
	if got := deployedIn(t, owner, wf); got["prod"] != 1 {
		t.Fatalf("after promoting: %v", got)
	}
	owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"environment": "prod"})

	// Version 2 reaches dev and staging; prod keeps 1 until promoted.
	digest := owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1", nil)["version"].(map[string]any)["digest"]
	owner.must(201, "POST", "/v1/workflows/"+wf+"/versions", map[string]any{"definition": hookFlow("2"), "parent_digest": digest})
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/2/publish", nil)
	if got := deployedIn(t, owner, wf); got["staging"] != 2 || got["prod"] != 1 {
		t.Fatalf("after publishing 2: %v", got)
	}
	if got := triggerVersions(t, owner, wf); got["prod"] != 1 || got["staging"] != 2 {
		t.Errorf("webhooks after publishing 2: %v", got)
	}
	owner.must(409, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"environment": "prod", "version": 2})

	// With four-eyes, the author cannot promote their own version.
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true})
	owner.must(403, "POST", "/v1/workflows/"+wf+"/promote", map[string]any{"from": "staging", "to": "prod"})
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "rev@acme.test", "password": "correct horse battery", "roles": []string{"admin"}})
	rev := w.login(t, "rev@acme.test", "correct horse battery")
	rev.must(200, "POST", "/v1/workflows/"+wf+"/promote", map[string]any{"from": "staging", "to": "prod"})
	if got := deployedIn(t, owner, wf); got["prod"] != 2 {
		t.Fatalf("after the second promotion: %v", got)
	}
	if !strings.Contains(toJSON(owner.must(200, "GET", "/v1/audit", nil)), `"workflow.promote"`) {
		t.Error("promotion not audited")
	}
	envs := toJSON(owner.must(200, "GET", "/v1/environments", nil))
	if !strings.Contains(envs, `"name":"prod","promotion_from":"staging"`) {
		t.Errorf("environments: %s", envs)
	}
}
