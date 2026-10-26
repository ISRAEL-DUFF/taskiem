package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/ingest"
)

const basicHookFlow = `{"schema":"wd/v1","id":"wf_hook","version":1,"name":"hook",
  "trigger":{"type":"webhook","config":{"path":"/shop","auth":"basic","methods":["GET","POST"]}},
  "steps":[{"id":"a","type":"transform","config":{"output":"=trigger.body"}}]}`

func TestWebhookListen(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	wf := publishFlow(t, owner, basicHookFlow)

	// Basic auth's signing secret is a user name and password.
	sec := owner.must(201, "POST", "/v1/workflows/"+wf+"/signing-secret", map[string]any{"environment": "prod"})
	secret := sec["secret"].(string)
	if sec["auth"] != "basic" || !strings.HasPrefix(secret, "taskiem:") {
		t.Fatalf("basic signing secret: %v", sec)
	}

	owner.must(400, "POST", "/v1/workflows/"+wf+"/webhook-listens", map[string]any{"config": map[string]any{"path": "/connectors/x", "auth": "none"}})
	owner.must(400, "POST", "/v1/workflows/"+wf+"/webhook-listens", map[string]any{})
	owner.must(404, "POST", "/v1/workflows/00000000-0000-0000-0000-000000000000/webhook-listens", map[string]any{"config": map[string]any{"path": "/a", "auth": "none"}})
	// The config as it stands in the editor, not yet saved: GET only now.
	l := owner.must(201, "POST", "/v1/workflows/"+wf+"/webhook-listens", map[string]any{"config": map[string]any{"path": "/shop", "auth": "basic", "methods": []string{"GET"}}})
	url, id := l["url"].(string), l["id"].(string)
	if !strings.HasPrefix(url, "/hooks/") || !strings.Contains(url, "/_listen/"+id+"/") {
		t.Fatalf("test address: %v", l)
	}
	if st := owner.must(200, "GET", "/v1/workflows/"+wf+"/webhook-listens/"+id, nil); st["status"] != "waiting" {
		t.Errorf("before the call: %v", st)
	}

	// The call, through the edge.
	h := &ingest.Handler{Store: w.env.Store, Secrets: w.env.Vault, Connections: w.env.Vault, Registry: w.env.Registry}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", h))
	edge := httptest.NewServer(mux)
	t.Cleanup(edge.Close)
	req, _ := http.NewRequest("POST", edge.URL+url, bytes.NewReader([]byte(`{"order":"A1"}`)))
	req.Header.Set("Content-Type", "application/json")
	user, pw, _ := strings.Cut(secret, ":")
	req.SetBasicAuth(user, pw)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("the test call: %d", resp.StatusCode)
	}

	st := owner.must(200, "GET", "/v1/workflows/"+wf+"/webhook-listens/"+id, nil)
	raw, _ := json.Marshal(st)
	if st["status"] != "captured" || !strings.Contains(string(raw), `"order":"A1"`) || !strings.Contains(string(raw), `"auth":"ok"`) ||
		!strings.Contains(string(raw), "takes GET, not POST") || strings.Contains(string(raw), pw) {
		t.Errorf("the capture: %s", raw)
	}
	// Another workflow's id does not read it.
	other := publishFlow(t, owner, loanFlow)
	owner.must(404, "GET", "/v1/workflows/"+other+"/webhook-listens/"+id, nil)
}
