package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/xenibase"
	"github.com/israel-duff/taskiem/engine/ai"
	"github.com/israel-duff/taskiem/engine/ai/builder"
)

// needsOf indexes a readiness answer's needs by kind/name/environment.
func needsOf(t *testing.T, v any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, n := range v.([]any) {
		n := n.(map[string]any)
		out[n["kind"].(string)+"/"+n["name"].(string)+"/"+n["environment"].(string)] = n
	}
	return out
}

// An AI draft that needs Paystack, a variable and an approver is made
// ready from its review (no-technical-steps N3): the proposal lists each
// need with what fixes it, publishing refuses while a step would fail at
// once, and each fix clears its need.
func TestReadiness(t *testing.T) {
	doc := strings.Replace(builder.ExampleWD, `"reason":"Loan disbursement"`, `"reason":"=env.payout_note"`, 1)
	w, f := aiWorld(t, aiEnvelope(t, doc))
	owner := w.tenant(t, "Acme", "owner@acme.test")

	b := build(t, w, owner, map[string]any{"goal": "pay approved loans with Paystack"})
	prop := b["proposal"].(map[string]any)
	needs := needsOf(t, prop["needs"])
	conn, ok := needs["connection/main/prod"]
	if !ok || conn["connector"] != "paystack" || conn["service"] != "Paystack" || conn["problem"] != "missing" || conn["blocking"] != true ||
		conn["message"] != "Connect Paystack." || conn["steps"].([]any)[0].(map[string]any)["id"] != "pay" {
		t.Fatalf("the connection need: %v (all: %v)", conn, prop["needs"])
	}
	if dev := needs["connection/main/dev"]; dev == nil || dev["blocking"] != false {
		t.Errorf("dev's gap does not block: %v", dev)
	}
	if v := needs["variable/payout_note/prod"]; v == nil || v["blocking"] != false || v["message"] != "Fill in payout note." {
		t.Errorf("the variable need: %v", v)
	}
	if r := needs["role/credit_officer/prod"]; r == nil || r["problem"] != "nobody" || r["blocking"] != false {
		t.Errorf("the role need: %v (all: %v)", r, prop["needs"])
	}
	// The builder was told the roles the business has, and its size.
	prompt := ai.PromptText(f.Requests()[0])
	if !strings.Contains(prompt, `"roles": [`) || !strings.Contains(prompt, `"holders": 1`) || !strings.Contains(prompt, `"members": 1`) {
		t.Errorf("the prompt lacks the roles: %s", prompt[strings.Index(prompt, "<context>"):])
	}

	// The same from a definition not saved.
	r := owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})
	if r["ready"] != false || len(r["needs"].([]any)) != len(prop["needs"].([]any)) {
		t.Fatalf("draft readiness: %v", r)
	}

	// Saved, it cannot be published while Paystack is not connected; the
	// answer says what to fix.
	wf := owner.must(201, "POST", "/v1/ai/builds/"+b["id"].(string)+"/save", map[string]any{"name": "Loan payouts"})["id"].(string)
	st, body := owner.do("POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	if st != 422 || body["code"] != "not_ready" {
		t.Fatalf("publish without Paystack: %d %v", st, body)
	}
	if got := needsOf(t, body["needs"]); len(got) != 1 || got["connection/main/prod"] == nil {
		t.Fatalf("the blocking needs: %v", body["needs"])
	}

	// Each fix, from the review: connect, set, give the role.
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main",
		"credentials": map[string]string{"secret_key": "sk_test_abc"}})
	owner.must(204, "PUT", "/v1/variables/prod/payout_note", map[string]any{"value": "Loan payout"})
	cora := addMember(t, w, owner, "cora", "credit_officer")
	r = owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1/readiness", nil)
	got := needsOf(t, r["needs"])
	if r["ready"] != true || got["connection/main/prod"] != nil || got["variable/payout_note/prod"] != nil || got["role/credit_officer/prod"] != nil {
		t.Fatalf("after the fixes: %v", r)
	}
	if st := owner.must(200, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)["state"]; st != "published" {
		t.Fatalf("publish: %v", st)
	}

	// A business role lets its holder decide the approvals naming it,
	// without approval.decide; nobody else's.
	roles := owner.must(200, "GET", "/v1/approver-roles", nil)
	if roles["members"] != float64(2) || !strings.Contains(toJSON(roles["roles"]), `{"builtin":false,"holders":1,"name":"credit_officer"}`) {
		t.Errorf("approver roles: %v", roles)
	}
	flow := `{"schema":"wd/v1","id":"wf_credit","version":1,"name":"credit","trigger":{"type":"manual"},
	  "steps":[{"id":"ok","type":"approval","name":"Check the loan","config":{"role":"credit_officer"}},
	           {"id":"other","type":"approval","needs":["ok"],"config":{"role":"head_of_credit"}}]}`
	wf2 := publishFlow(t, owner, flow)
	run := owner.must(201, "POST", "/v1/workflows/"+wf2+"/runs", map[string]any{"input": map[string]any{}})["run_id"].(string)
	w.env.Drain(t)
	inbox := cora.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any)
	if len(inbox) != 1 || inbox[0].(map[string]any)["step_name"] != "Check the loan" {
		t.Fatalf("cora's inbox: %v", inbox)
	}
	if res := cora.must(200, "POST", "/v1/approvals/"+run+"/ok", map[string]any{"decision": "approved"}); res["status"] != "approved" {
		t.Fatalf("cora decides: %v", res)
	}
	w.env.Drain(t)
	if inbox := cora.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any); len(inbox) != 0 {
		t.Errorf("cora sees another role's approval: %v", inbox)
	}
	if st, _ := cora.do("POST", "/v1/approvals/"+run+"/other", map[string]any{"decision": "approved"}); st != 403 {
		t.Errorf("cora decides another role's approval: %d", st)
	}
}

// A missing table comes with the columns the workflow uses, so it can be
// made in one tap.
func TestReadinessDescribesMissingTables(t *testing.T) {
	w := newWorld(t)
	if err := w.env.Registry.Register(xenibase.New(xenibase.Options{})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Acme", "owner@acme.test")
	doc := `{"schema":"wd/v1","id":"wf_leave","version":1,"name":"Leave",
	 "trigger":{"type":"form","config":{"title":"Leave","access":"public"}},
	 "inputs":{"schema":{"type":"object","properties":{"name":{"type":"string"},"start_date":{"type":"string","format":"date"}}}},
	 "steps":[{"id":"save","type":"connector","name":"Save it","connector":"xenibase@1","action":"insert_rows","connection":"data_tables",
	   "input":{"table":"leave_requests","row":{"name":"=trigger.body.name","start_date":"=trigger.body.start_date","status":"pending"}}}]}`
	r := owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(doc)})
	for _, x := range r["needs"].([]any) {
		n := x.(map[string]any)
		if n["kind"] != "table" {
			continue
		}
		if got := toJSON(n["table"]); !strings.Contains(got, `"name":"leave_requests"`) || !strings.Contains(got, `{"name":"start_date","required":false,"title":"Start date","type":"date"}`) ||
			!strings.Contains(got, `"name":"status"`) {
			t.Fatalf("the table: %s", got)
		}
		return
	}
	t.Fatalf("no table need: %v", r["needs"])
}
