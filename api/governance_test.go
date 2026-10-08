package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/totp"
)

const highValuePolicy = `{"rules":[
  {"when":"=subject.amount < 1000000","levels":[{"role":"credit_officer"}]},
  {"when":"=subject.amount >= 1000000","levels":[{"role":"credit_officer"},{"role":"head_of_credit","count":2}],"step_up":"totp"}]}`

const policyFlow = `{"schema":"wd/v1","id":"wf_disburse","version":1,"name":"disburse","trigger":{"type":"manual"},
  "steps":[{"id":"ok","type":"approval","config":{"policy":"high_value","subject":{"amount":"=trigger.body.amount"}}},
           {"id":"done","type":"transform","needs":["ok"],"config":{"output":"=steps.ok.output.decision"}}]}`

// enrol enrols a member's authenticator and returns the secret.
func enrol(t *testing.T, c *client) string {
	t.Helper()
	secret := c.must(200, "POST", "/v1/me/totp", map[string]any{"password": testPassword})["secret"].(string)
	code, _ := totp.Code(secret, totp.Step(time.Now()))
	c.must(204, "POST", "/v1/me/totp/confirm", map[string]any{"code": code})
	return secret
}

// codeAfter returns a code for a later time step than any used so far.
func codeAfter(secret string, steps int64) string {
	c, _ := totp.Code(secret, totp.Step(time.Now())+steps)
	return c
}

func TestPolicyLevelsStepUpAndDelegation(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	people := map[string][]string{
		"alice": {"approver", "credit_officer", "head_of_credit"},
		"bob":   {"approver", "head_of_credit"},
		"carol": {"approver", "head_of_credit"},
		"dave":  {"approver"},
	}
	c := map[string]*client{}
	for name, roles := range people {
		owner.must(201, "POST", "/v1/members", map[string]any{"email": name + "@acme.test", "password": "correct horse battery", "roles": roles})
		c[name] = w.login(t, name+"@acme.test", "correct horse battery")
	}

	// A workflow naming a policy that does not exist cannot be published.
	created := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "disburse", "definition": json.RawMessage(policyFlow)})
	wf := created["id"].(string)
	status, body := owner.do("POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	if status != 422 || !strings.Contains(toJSON(body), "has no active version") {
		t.Fatalf("publish without the policy: %d %v", status, body)
	}
	owner.must(422, "PUT", "/v1/policies/high_value", map[string]any{"document": json.RawMessage(`{"rules":[]}`)})
	owner.must(201, "PUT", "/v1/policies/high_value", map[string]any{"document": json.RawMessage(highValuePolicy)})
	owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 5000000}})["run_id"].(string)
	path := "/v1/approvals/" + run + "/ok"

	// Level 1: credit officers.
	if inbox := c["bob"].must(200, "GET", "/v1/approvals", nil)["approvals"].([]any); len(inbox) != 0 {
		t.Errorf("level 2's approvers see the step before level 1 is done: %v", inbox)
	}
	item := c["alice"].must(200, "GET", "/v1/approvals", nil)["approvals"].([]any)[0].(map[string]any)
	if item["level"] != float64(1) || item["levels"] != float64(2) || item["step_up"] != "totp" {
		t.Fatalf("inbox item: %v", item)
	}
	// Step-up is required, and needs an enrolled authenticator.
	status, body = c["alice"].do("POST", path, map[string]any{"decision": "approved"})
	if status != 403 || body["step_up"] != "totp" {
		t.Fatalf("without step-up: %d %v", status, body)
	}
	alice := enrol(t, c["alice"])
	res := c["alice"].must(200, "POST", path, map[string]any{"decision": "approved", "totp": codeAfter(alice, 1)})
	if res["status"] != "open" || res["level"] != float64(2) {
		t.Fatalf("after level 1: %v", res)
	}
	// Alice holds head_of_credit too, but approved level 1.
	status, body = c["alice"].do("POST", path, map[string]any{"decision": "approved"})
	if status != 403 || !strings.Contains(body["error"].(string), "earlier level") {
		t.Errorf("same person, two levels: %d %v", status, body)
	}

	// Level 2: two heads of credit. Bob votes; a replayed code is refused.
	bob := enrol(t, c["bob"])
	code := codeAfter(bob, 1)
	c["bob"].must(200, "POST", path, map[string]any{"decision": "approved", "totp": code})
	status, _ = c["bob"].do("POST", path, map[string]any{"decision": "approved", "totp": code})
	if status != 403 {
		t.Errorf("a replayed code: %d", status)
	}

	// Carol is away: she delegates head_of_credit to Dave for a week.
	c["carol"].must(400, "POST", "/v1/delegations", map[string]any{"to": "dave@acme.test", "roles": []string{"head_of_credit"}, "ends_at": time.Now().Add(100 * 24 * time.Hour)})
	c["carol"].must(403, "POST", "/v1/delegations", map[string]any{"to": "dave@acme.test", "roles": []string{"credit_officer"}, "ends_at": time.Now().Add(time.Hour), "reason": "leave"})
	c["carol"].must(201, "POST", "/v1/delegations", map[string]any{"to": "dave@acme.test", "roles": []string{"head_of_credit"},
		"ends_at": time.Now().Add(7 * 24 * time.Hour), "reason": "annual leave"})
	inbox := c["dave"].must(200, "GET", "/v1/approvals", nil)["approvals"].([]any)
	if len(inbox) != 1 || inbox[0].(map[string]any)["on_behalf_of"] != "carol@acme.test" {
		t.Fatalf("dave's inbox: %v", inbox)
	}
	dave := enrol(t, c["dave"])
	res = c["dave"].must(200, "POST", path, map[string]any{"decision": "approved", "totp": codeAfter(dave, 1)})
	if res["status"] != "approved" {
		t.Fatalf("after level 2: %v", res)
	}
	// Carol cannot also vote: Dave voted for her.
	runOut := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if runOut["run"].(map[string]any)["status"] != "completed" {
		t.Errorf("run: %v", runOut["run"])
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit?action=approval.decide", nil))
	if !strings.Contains(audit, `"step_up":"totp"`) {
		t.Errorf("approval audit lacks the step-up: %s", audit)
	}

	// A small amount needs one credit officer, without step-up.
	small := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 10}})["run_id"].(string)
	if c["alice"].must(200, "POST", "/v1/approvals/"+small+"/ok", map[string]any{"decision": "approved"})["status"] != "approved" {
		t.Error("small amount")
	}

	// The approvals report shows every decision, who it was for, and bands.
	rep := owner.must(200, "GET", "/v1/reports/approvals", nil)
	rows := rep["rows"].([]any)
	if len(rows) != 4 {
		t.Fatalf("approval rows: %v", rows)
	}
	var daveRow map[string]any
	for _, r := range rows {
		if m := r.(map[string]any); m["approver"] == "dave@acme.test" {
			daveRow = m
		}
	}
	if daveRow == nil || daveRow["on_behalf_of"] != "carol@acme.test" || daveRow["level"] != float64(2) || daveRow["step_up"] != "totp" || daveRow["band"] != "₦1m – ₦10m" {
		t.Errorf("dave's row: %v", daveRow)
	}
	if !strings.Contains(toJSON(rep["summary"]), `"band":"under ₦100k"`) {
		t.Errorf("summary: %v", rep["summary"])
	}
	csv := owner.raw(t, "GET", "/v1/reports/approvals?format=csv")
	if !strings.HasPrefix(csv, "decided_at,approver,on_behalf_of,decision,level") || !strings.Contains(csv, "dave@acme.test,carol@acme.test,approved,2,totp") {
		t.Errorf("csv:\n%s", csv)
	}
	changes := owner.must(200, "GET", "/v1/reports/changes", nil)["rows"].([]any)
	if len(changes) != 1 || changes[0].(map[string]any)["published_by"] == nil {
		t.Errorf("changes: %v", changes)
	}
	chain := owner.must(200, "GET", "/v1/reports/chain", nil)["summary"].(map[string]any)
	if chain["intact"] != true || chain["entries"].(float64) < 10 {
		t.Errorf("chain: %v", chain)
	}
	// An anchor whose hash is not the chain's at that entry: the chain was
	// rewritten after it, though it verifies on its own.
	var tenant uuid.UUID
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT id FROM tenants WHERE name = 'Acme'`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO audit_anchors (tenant_id, chain_seq, head_hash, anchored_at, key_id, signature) VALUES ($1, 3, $2, now(), 'k1', '\x01')`,
		tenant, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	rep = owner.must(200, "GET", "/v1/reports/chain", nil)
	chain = rep["summary"].(map[string]any)
	if chain["intact"] != false || chain["anchors_mismatched"] != float64(1) || rep["rows"].([]any)[0].(map[string]any)["matches_chain"] != false {
		t.Errorf("rewritten chain: %v %v", chain, rep["rows"])
	}
	owner.must(200, "GET", "/v1/runs/"+run+"?reveal=true", nil)
	if pii := owner.must(200, "GET", "/v1/reports/pii?format=json", nil)["rows"].([]any); len(pii) != 1 || pii[0].(map[string]any)["action"] != "pii.reveal" {
		t.Errorf("pii access: %v", pii)
	}
	if eff := owner.must(200, "GET", "/v1/reports/effects?from=2020-01-01&to=2099-01-01", nil); len(eff["rows"].([]any)) != 0 {
		t.Errorf("effects: %v", eff)
	}
	c["dave"].must(403, "GET", "/v1/reports/approvals", nil) // audit.read only
	owner.must(400, "GET", "/v1/reports/approvals?from=yesterday", nil)
	owner.must(404, "GET", "/v1/reports/salaries", nil)
}

func TestFourEyesOnPublishingAndPolicies(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "pat@acme.test", "password": "correct horse battery", "roles": []string{"admin"}})
	owner.must(201, "POST", "/v1/members", map[string]any{"email": "ben@acme.test", "password": "correct horse battery", "roles": []string{"builder"}})
	pat, ben := w.login(t, "pat@acme.test", "correct horse battery"), w.login(t, "ben@acme.test", "correct horse battery")

	pat.must(403, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true}) // owners only
	owner.must(200, "PUT", "/v1/governance", map[string]any{"four_eyes_publish": true, "four_eyes_policies": true})

	// A policy edit waits for someone other than its author.
	owner.must(201, "PUT", "/v1/policies/basic", map[string]any{"document": json.RawMessage(`{"rules":[{"levels":[{"role":"approver"}]}]}`)})
	owner.must(403, "POST", "/v1/policies/basic/versions/1/approve", nil)
	pat.must(200, "POST", "/v1/policies/basic/versions/1/approve", nil)
	if p := owner.must(200, "GET", "/v1/policies", nil)["policies"].([]any); len(p) != 1 || p[0].(map[string]any)["state"] != "active" {
		t.Fatalf("policies: %v", p)
	}
	if g := owner.must(200, "GET", "/v1/governance", nil); g["four_eyes_policies"] != true {
		t.Errorf("governance: %v", g)
	}
	// A second edit, rejected, leaves the first in force.
	owner.must(201, "PUT", "/v1/policies/basic", map[string]any{"document": json.RawMessage(`{"rules":[{"levels":[{"role":"approver","count":2}]}]}`)})
	if out := pat.must(200, "POST", "/v1/policies/basic/versions/2/reject", nil); out["state"] != "rejected" {
		t.Errorf("reject: %v", out)
	}
	if v := owner.must(200, "GET", "/v1/policies/basic", nil)["versions"].([]any); len(v) != 2 || v[1].(map[string]any)["state"] != "active" {
		t.Errorf("versions: %v", v)
	}

	// Ben writes a workflow; publishing waits for a second publisher.
	wf := ben.must(201, "POST", "/v1/workflows", map[string]any{"name": "x", "definition": json.RawMessage(flowDoc("wf_x", "x", "=1"))})["id"].(string)
	pending := pat.must(202, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	if pending["state"] != "pending_approval" {
		t.Fatalf("publish: %v", pending)
	}
	pat.must(403, "POST", "/v1/workflows/"+wf+"/versions/1/publish/approve", nil) // the requester
	if reqs := owner.must(200, "GET", "/v1/publish-requests", nil)["requests"].([]any); len(reqs) != 1 {
		t.Fatalf("requests: %v", reqs)
	}
	out := owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish/approve", map[string]any{"comment": "looks right"})
	if out["state"] != "published" {
		t.Fatalf("approve: %v", out)
	}
	if v := owner.must(200, "GET", "/v1/workflows/"+wf, nil)["workflow"].(map[string]any)["active_version"]; v != float64(1) {
		t.Errorf("active version: %v", v)
	}
	// A rejected request leaves the version a draft.
	wf2 := ben.must(201, "POST", "/v1/workflows", map[string]any{"name": "y", "definition": json.RawMessage(flowDoc("wf_y", "y", "=1"))})["id"].(string)
	pat.must(202, "POST", "/v1/workflows/"+wf2+"/versions/1/publish", nil)
	if out := owner.must(200, "POST", "/v1/workflows/"+wf2+"/versions/1/publish/reject", map[string]any{"comment": "not yet"}); out["state"] != "draft" {
		t.Errorf("reject: %v", out)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{"governance.change", "policy.approve", "publish_request.create", "publish_request.approve"} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}
}
