package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/wasmconn/wasmtest"
)

func (c *client) upload(manifest, module []byte) (int, map[string]any) {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, b := range map[string][]byte{"manifest": manifest, "module": module} {
		fw, _ := mw.CreateFormFile(name, name)
		_, _ = fw.Write(b)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", c.base+"/v1/tenant-connectors", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

const ledgerFlow = `{"schema":"wd/v1","id":"wf_pay","version":1,"name":"pay","trigger":{"type":"manual"},
  "steps":[{"id":"pay","type":"connector","connector":"x_example_ledger@1","action":"create_payment",
    "connection":"main","input":{"amount":"=trigger.body.amount","account_number":"=trigger.body.account_number"}}]}`

func TestTenantConnectorUploadAndRun(t *testing.T) {
	manifest, module := wasmtest.Example(t)
	var mu sync.Mutex
	paid := map[string]bool{}
	ledger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		ref, _ := b["reference"].(string)
		if r.Header.Get("Authorization") != "Bearer ledger-key" || r.URL.Path != "/v1/payments" || !strings.HasPrefix(ref, "tsk_") || paid[ref] {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		paid[ref] = true
		_, _ = w.Write([]byte(`{"id":"pay_9","reference":"` + ref + `","status":"pending"}`))
	}))
	defer ledger.Close()
	w := newWorld(t)
	w.env.Connectors.BaseURLs = map[string]string{"x_example_ledger": ledger.URL}
	acme := w.tenant(t, "Acme", "owner@acme.test")
	globex := w.tenant(t, "Globex", "owner@globex.test")

	// Only connector.manage may upload; nonsense is refused with the reason.
	acme.must(201, "POST", "/v1/members", map[string]any{"email": "bld@acme.test", "password": "correct horse battery", "roles": []string{"builder"}})
	bld := w.login(t, "bld@acme.test", "correct horse battery")
	if st, _ := bld.upload(manifest, module); st != 403 {
		t.Errorf("builder upload: %d", st)
	}
	if st, out := acme.upload(manifest, []byte("not wasm")); st != 400 || !strings.Contains(toJSON(out), "does not compile") {
		t.Errorf("bad module: %d %v", st, out)
	}

	st, out := acme.upload(manifest, module)
	if st != 201 || out["ref"] != "x_example_ledger@1" {
		t.Fatalf("upload: %d %v", st, out)
	}
	if st, out := acme.upload(manifest, module); st != 409 || !strings.Contains(toJSON(out), "raise the version") {
		t.Errorf("same version again: %d %v", st, out)
	}
	list := acme.must(200, "GET", "/v1/tenant-connectors", nil)["connectors"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["active"] != true {
		t.Errorf("list: %v", list)
	}
	if !strings.Contains(toJSON(acme.must(200, "GET", "/v1/connectors", nil)), "x_example_ledger@1") {
		t.Error("the palette does not offer the tenant's connector")
	}

	// Another tenant can neither see nor use it.
	if strings.Contains(toJSON(globex.must(200, "GET", "/v1/connectors", nil)), "x_example_ledger") ||
		len(globex.must(200, "GET", "/v1/tenant-connectors", nil)["connectors"].([]any)) != 0 {
		t.Error("globex sees acme's connector")
	}
	if probs := globex.must(200, "POST", "/v1/validate", map[string]any{"definition": json.RawMessage(ledgerFlow)})["problems"].([]any); len(probs) == 0 {
		t.Error("globex may use acme's connector")
	}

	// Acme pays through it, end to end.
	acme.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "x_example_ledger@1", "name": "main",
		"credentials": map[string]string{"api_key": "ledger-key"}})
	wf := publishFlow(t, acme, ledgerFlow)
	run := acme.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 5000, "account_number": "0123456789"}})["run_id"].(string)
	w.env.Drain(t)
	got := acme.must(200, "GET", "/v1/runs/"+run, nil)
	if st := got["run"].(map[string]any)["status"]; st != "completed" || !strings.Contains(toJSON(got), "pay_9") {
		t.Fatalf("run: %v\n%s", st, toJSON(got["events"]))
	}
	if strings.Contains(toJSON(got), "0123456789") {
		t.Error("the account number the manifest declares personal is shown in the clear")
	}

	// Disabled, it is gone from new definitions.
	acme.must(204, "POST", "/v1/tenant-connectors/x_example_ledger/1.0.0/disable", nil)
	if probs := acme.must(200, "POST", "/v1/validate", map[string]any{"definition": json.RawMessage(ledgerFlow)})["problems"].([]any); len(probs) == 0 {
		t.Error("a disabled connector still validates")
	}
	acme.must(404, "POST", "/v1/tenant-connectors/x_example_ledger/1.0.0/disable", nil)
}
