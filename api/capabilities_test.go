package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/paystack"
)

const payByAnyRail = `{"schema":"wd/v1","id":"wf_payany","version":1,"name":"pay","trigger":{"type":"manual"},"steps":[
  {"id":"pay","type":"capability","uses":"pay_bank","connection":"main",
   "input":{"amount_kobo":500000,"bank_code":"058","account_number":"0123456789","account_name":"Bola Ade","narration":"Salary"}}]}`

// A capability step publishes only where a connection can do it, and the
// deployment records the rail.
func TestCapabilityStepsDeploy(t *testing.T) {
	w := newWorld(t)
	if err := w.env.Registry.Register(paystack.New(paystack.Options{})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")

	caps := owner.must(200, "GET", "/v1/capabilities", nil)["capabilities"].([]any)
	found := false
	for _, c := range caps {
		m := c.(map[string]any)
		if m["name"] == "pay_bank" {
			found = strings.Contains(toJSON(m["rails"]), "paystack@1") && m["title"] == "Pay a bank account"
		}
	}
	if !found {
		t.Fatalf("capabilities: %s", toJSON(caps))
	}

	out := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "pay", "definition": json.RawMessage(payByAnyRail)})
	id := out["id"].(string)
	// Readiness says what it needs, as a run would find it.
	need := owner.must(200, "GET", "/v1/workflows/"+id+"/versions/1/readiness", nil)
	if !strings.Contains(toJSON(need), `"capability":"pay_bank"`) || !strings.Contains(toJSON(need), `"paystack"`) || !strings.Contains(toJSON(need), `"problem":"missing"`) {
		t.Fatalf("readiness: %s", toJSON(need))
	}
	// Not connected yet: publishing waits for it, as for any connection.
	if e := owner.must(422, "POST", "/v1/workflows/"+id+"/versions/1/publish", nil); e["code"] != "not_ready" {
		t.Fatalf("publish without the connection: %v", e)
	}
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	owner.must(200, "POST", "/v1/workflows/"+id+"/versions/1/publish", nil)
	// A run in an environment that is not connected is refused in plain words.
	if e := owner.must(409, "POST", "/v1/workflows/"+id+"/runs", map[string]any{"environment": "dev", "input": map[string]any{}})["error"].(string); !strings.Contains(e, `there is no connection called "main"`) {
		t.Fatalf("a run without the connection: %q", e)
	}
	st, run := owner.do("POST", "/v1/workflows/"+id+"/runs", map[string]any{"environment": "prod", "input": map[string]any{}})
	if st/100 != 2 || run["run_id"] == nil {
		t.Fatalf("a run once connected: %d %v", st, run)
	}
	// A workflow published once connected records the rail it runs on.
	id = publishFlow(t, owner, strings.Replace(payByAnyRail, "wf_payany", "wf_payany2", 1))
	var rails string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT rails::text FROM deployments WHERE workflow_id = $1 AND environment = 'prod'`, id).Scan(&rails); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rails, `"connector": "paystack@1"`) || !strings.Contains(rails, `"step": "pay"`) {
		t.Errorf("rails %s", rails)
	}
}
