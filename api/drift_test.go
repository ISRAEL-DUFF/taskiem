package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/wasmconn/wasmtest"
)

// TestConnectorDriftIsRecorded: a provider answers with a status the
// manifest does not list; the run completes, and the drift is recorded,
// counted, listed and acknowledged.
func TestConnectorDriftIsRecorded(t *testing.T) {
	manifest, module := wasmtest.Example(t)
	ledger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		_, _ = w.Write([]byte(`{"id":"pay_9","reference":"` + b["reference"].(string) + `","status":"settled"}`))
	}))
	defer ledger.Close()
	w := newWorld(t)
	w.env.Connectors.BaseURLs = map[string]string{"x_example_ledger": ledger.URL}
	acme := w.tenant(t, "Acme", "owner@acme.test")
	if st, out := acme.upload(manifest, module); st != 201 {
		t.Fatalf("upload: %d %v", st, out)
	}
	acme.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "x_example_ledger@1", "name": "main",
		"credentials": map[string]string{"api_key": "k"}})
	wf := publishFlow(t, acme, ledgerFlow)
	for range 2 {
		acme.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 1, "account_number": "0123456789"}})
	}
	w.env.Drain(t)

	list := acme.must(200, "GET", "/v1/connector-drift", nil)["drift"].([]any)
	if len(list) != 1 {
		t.Fatalf("drift: %v", list)
	}
	d := list[0].(map[string]any)
	if d["path"] != "/status" || d["kind"] != "enum" || d["observed"] != `"settled"` || d["occurrences"] != float64(2) || d["version"] != "1.0.0" {
		t.Errorf("finding: %v", d)
	}
	if !strings.Contains(d["expected"].(string), `"completed"`) {
		t.Errorf("expected: %v", d["expected"])
	}
	ack := map[string]any{"connector": d["connector"], "version": d["version"], "action": d["action"], "path": d["path"], "kind": d["kind"]}
	acme.must(204, "POST", "/v1/connector-drift/acknowledge", ack)
	acme.must(404, "POST", "/v1/connector-drift/acknowledge", ack)
	globex := w.tenant(t, "Globex", "owner@globex.test")
	if n := len(globex.must(200, "GET", "/v1/connector-drift", nil)["drift"].([]any)); n != 0 {
		t.Errorf("globex sees %d of acme's findings", n)
	}
}
