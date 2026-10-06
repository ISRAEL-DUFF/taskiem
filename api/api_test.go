package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/api"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// client calls the API as one principal.
type client struct {
	t     *testing.T
	base  string
	token string
}

func (c *client) do(method, path string, body any, hdr ...string) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"raw": string(raw)}
	}
	return resp.StatusCode, out
}

func (c *client) must(want int, method, path string, body any, hdr ...string) map[string]any {
	c.t.Helper()
	got, out := c.do(method, path, body, hdr...)
	if got != want {
		c.t.Fatalf("%s %s: status %d, want %d: %v", method, path, got, want, out)
	}
	return out
}

type world struct {
	env  *rt.Env
	base string
	srv  *api.Server
}

func newWorld(t *testing.T) *world {
	e := rt.New(t)
	srv := &api.Server{Store: e.Store, Vault: e.Vault, Registry: e.Registry, AllowSignup: true, Egress: e.Egress}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &world{env: e, base: ts.URL, srv: srv}
}

// tenant signs up a new tenant and returns its owner's client.
func (w *world) tenant(t *testing.T, name, email string) *client {
	anon := &client{t: t, base: w.base}
	anon.must(201, "POST", "/v1/signup", map[string]any{"tenant": name, "email": email, "name": "Owner", "password": "correct horse battery"})
	return w.login(t, email, "correct horse battery")
}

func (w *world) login(t *testing.T, email, pw string) *client {
	anon := &client{t: t, base: w.base}
	out := anon.must(200, "POST", "/v1/auth/login", map[string]any{"email": email, "password": pw})
	return &client{t: t, base: w.base, token: out["token"].(string)}
}

const loanFlow = `{"schema":"wd/v1","id":"wf_loan","version":1,"name":"loan","trigger":{"type":"manual"},
  "inputs":{"schema":{"$ref":"#/types/Loan"}},
  "types":{"Loan":{"type":"object","required":["bvn","amount"],"properties":{
    "bvn":{"type":"string","x-pii":"bvn"},"amount":{"type":"integer","minimum":1}}}},
  "steps":[
    {"id":"ok","type":"approval","config":{"role":"credit_officer","count":2,"subject":{"bvn":"=trigger.body.bvn","amount":"=trigger.body.amount"}}},
    {"id":"done","type":"transform","needs":["ok"],"config":{"output":{"decision":"=steps.ok.output.decision"}}}]}`

func publishFlow(t *testing.T, c *client, doc string) string {
	t.Helper()
	out := c.must(201, "POST", "/v1/workflows", map[string]any{"name": "loan", "definition": json.RawMessage(doc)})
	if probs := out["problems"].([]any); len(probs) > 0 {
		t.Fatalf("problems: %v", probs)
	}
	id := out["id"].(string)
	c.must(200, "POST", "/v1/workflows/"+id+"/versions/1/publish", nil)
	return id
}

func TestAuthAndCSRF(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	me := owner.must(200, "GET", "/v1/me", nil)
	if !strings.Contains(toJSON(me["permissions"]), "workflow.publish") {
		t.Fatalf("owner permissions: %v", me["permissions"])
	}

	anon := &client{t: t, base: w.base}
	if st, _ := anon.do("GET", "/v1/me", nil); st != 401 {
		t.Errorf("anonymous /me: %d", st)
	}
	if st, _ := anon.do("POST", "/v1/auth/login", map[string]any{"email": "owner@acme.test", "password": "wrong password!!"}); st != 401 {
		t.Errorf("bad password: %d", st)
	}

	// Cookie sessions need the CSRF header on mutations.
	req, _ := http.NewRequest("POST", w.base+"/v1/validate", strings.NewReader(`{"definition":{}}`))
	req.AddCookie(&http.Cookie{Name: "taskiem_session", Value: owner.token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("cookie POST without %s: %d", "X-Taskiem-Request", resp.StatusCode)
	}
	req, _ = http.NewRequest("POST", w.base+"/v1/validate", strings.NewReader(`{"definition":{}}`))
	req.AddCookie(&http.Cookie{Name: "taskiem_session", Value: owner.token})
	req.Header.Set("X-Taskiem-Request", "1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("cookie POST with header: %d", resp.StatusCode)
	}

	owner.must(204, "POST", "/v1/auth/logout", nil)
	if st, _ := owner.do("GET", "/v1/me", nil); st != 401 {
		t.Errorf("after logout: %d", st)
	}
}

func TestWorkflowLifecycleAndRuns(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")

	// A draft may be incomplete; it cannot be published.
	bad := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "draft", "definition": json.RawMessage(`{"schema":"wd/v1","id":"x","version":1,"name":"x","trigger":{"type":"manual"},"steps":[{"id":"a","type":"connector","connector":"nope@1","action":"go"}]}`)})
	if len(bad["problems"].([]any)) == 0 {
		t.Fatal("expected problems for an unknown connector")
	}
	owner.must(422, "POST", "/v1/workflows/"+bad["id"].(string)+"/versions/1/publish", nil)

	wf := publishFlow(t, owner, loanFlow)
	owner.must(204, "PUT", "/v1/workflows/"+wf+"/versions/1/layout", map[string]any{"ok": map[string]any{"x": 10, "y": 20}})
	v := owner.must(200, "GET", "/v1/workflows/"+wf+"/versions/1", nil)
	if v["layout"] == nil || v["version"].(map[string]any)["state"] != "published" {
		t.Fatalf("version: %v", v)
	}

	// Input is checked against the inputs schema.
	owner.must(422, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"amount": 0}})

	// Idempotency-Key returns the same run.
	in := map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 5000}}
	a := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", in, "Idempotency-Key", "loan-1")
	b := owner.must(200, "POST", "/v1/workflows/"+wf+"/runs", in, "Idempotency-Key", "loan-1")
	if a["run_id"] != b["run_id"] || a["created"] != true || b["created"] != false {
		t.Fatalf("idempotent start: %v vs %v", a, b)
	}
	runs := owner.must(200, "GET", "/v1/runs?workflow="+wf, nil)["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("runs: %v", runs)
	}

	// History is sealed unless revealed, and revealing is audited.
	run := a["run_id"].(string)
	sealed := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if strings.Contains(toJSON(sealed), "22212345678") {
		t.Error("sealed history shows the BVN")
	}
	open := owner.must(200, "GET", "/v1/runs/"+run+"?reveal=true", nil)
	if !strings.Contains(toJSON(open), "22212345678") {
		t.Error("revealed history should show the BVN")
	}

	owner.must(200, "POST", "/v1/runs/"+run+"/cancel", nil)
	if st := owner.must(200, "GET", "/v1/runs/"+run, nil)["run"].(map[string]any)["status"]; st != "cancelled" {
		t.Errorf("after cancel: %v", st)
	}

	audit := owner.must(200, "GET", "/v1/audit", nil)
	for _, action := range []string{"workflow.publish", "pii.reveal", "run.cancel", "auth.login"} {
		if !strings.Contains(toJSON(audit), `"`+action+`"`) {
			t.Errorf("audit lacks %s", action)
		}
	}
	if v := owner.must(200, "GET", "/v1/audit/verify", nil); v["intact"] != true {
		t.Errorf("audit chain: %v", v)
	}
}

func TestApprovalsSeparationOfDuties(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	for _, m := range []struct {
		email string
		roles []string
	}{
		{"alice@acme.test", []string{"approver", "credit_officer"}},
		{"bob@acme.test", []string{"approver", "credit_officer"}},
		{"carol@acme.test", []string{"approver"}},
		{"vic@acme.test", []string{"viewer"}},
	} {
		owner.must(201, "POST", "/v1/members", map[string]any{"email": m.email, "password": "correct horse battery", "roles": m.roles})
	}
	alice, bob := w.login(t, "alice@acme.test", "correct horse battery"), w.login(t, "bob@acme.test", "correct horse battery")
	carol, vic := w.login(t, "carol@acme.test", "correct horse battery"), w.login(t, "vic@acme.test", "correct horse battery")

	wf := publishFlow(t, owner, loanFlow)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22212345678", "amount": 5000}})["run_id"].(string)
	path := "/v1/approvals/" + run + "/ok"

	// The inbox shows the opened subject to qualified approvers only.
	inbox := alice.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any)
	if len(inbox) != 1 || !strings.Contains(toJSON(inbox), "22212345678") {
		t.Fatalf("alice's inbox: %v", inbox)
	}
	if n := len(carol.must(200, "GET", "/v1/approvals", nil)["approvals"].([]any)); n != 0 {
		t.Errorf("carol lacks the role but sees %d approvals", n)
	}

	vic.must(403, "POST", path, map[string]any{"decision": "approved"})   // no approval.decide
	carol.must(403, "POST", path, map[string]any{"decision": "approved"}) // wrong role
	owner.must(403, "POST", path, map[string]any{"decision": "approved"}) // maker (and no role)
	vic.must(403, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)

	if st := alice.must(200, "POST", path, map[string]any{"decision": "approved"})["status"]; st != "open" {
		t.Fatalf("one of two approvals: %v", st)
	}
	alice.must(409, "POST", path, map[string]any{"decision": "approved"}) // counts once
	if st := bob.must(200, "POST", path, map[string]any{"decision": "approved"})["status"]; st != "approved" {
		t.Fatalf("two approvals: %v", st)
	}
	bob.must(409, "POST", path, map[string]any{"decision": "rejected"}) // closed
	w.env.Drain(t)
	got := owner.must(200, "GET", "/v1/runs/"+run, nil)
	if st := got["run"].(map[string]any)["status"]; st != "completed" {
		t.Errorf("run after approval: %v\n%s", st, toJSON(got["events"]))
	}
}

func TestTenantIsolationAndKeys(t *testing.T) {
	w := newWorld(t)
	acme := w.tenant(t, "Acme", "owner@acme.test")
	globex := w.tenant(t, "Globex", "owner@globex.test")
	wf := publishFlow(t, acme, loanFlow)
	globex.must(404, "GET", "/v1/workflows/"+wf, nil)
	globex.must(404, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "1", "amount": 1}})
	if n := len(globex.must(200, "GET", "/v1/workflows", nil)["workflows"].([]any)); n != 0 {
		t.Errorf("globex sees %d workflows", n)
	}

	// Keys: scoped permissions and environment; shown once; revocable.
	out := acme.must(201, "POST", "/v1/api-keys", map[string]any{"name": "ci", "permissions": []string{"run.start", "run.read"}, "environment": "dev"})
	key := &client{t: t, base: w.base, token: out["key"].(string)}
	in := map[string]any{"bvn": "22212345678", "amount": 10}
	key.must(403, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": in, "environment": "prod"})
	started := key.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": in})
	if started["environment"] != "dev" {
		t.Errorf("key run environment: %v", started["environment"])
	}
	key.must(403, "GET", "/v1/workflows", nil)
	key.must(403, "POST", "/v1/api-keys", map[string]any{"name": "x", "permissions": []string{"run.start"}})
	acme.must(204, "DELETE", "/v1/api-keys/"+out["id"].(string), nil)
	key.must(401, "GET", "/v1/runs", nil)

	// A builder cannot mint a key with permissions they lack.
	acme.must(201, "POST", "/v1/members", map[string]any{"email": "bld@acme.test", "password": "correct horse battery", "roles": []string{"builder"}})
	bld := w.login(t, "bld@acme.test", "correct horse battery")
	bld.must(403, "POST", "/v1/members", map[string]any{"email": "x@acme.test", "password": "correct horse battery", "roles": []string{"owner"}})
}

func TestAdminAndErasure(t *testing.T) {
	w := newWorld(t)
	owner := w.tenant(t, "Acme", "owner@acme.test")
	owner.must(204, "PUT", "/v1/secrets/prod/api_token", map[string]any{"value": "s3cr3t"})
	if s := toJSON(owner.must(200, "GET", "/v1/secrets", nil)); !strings.Contains(s, "api_token") || strings.Contains(s, "s3cr3t") {
		t.Errorf("secret list: %s", s)
	}
	owner.must(204, "PUT", "/v1/variables/prod/api_base", map[string]any{"value": "https://api.example"})
	owner.must(204, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "api.example.com"})
	owner.must(400, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "http://bad"})
	if s := toJSON(owner.must(200, "GET", "/v1/egress?environment=prod", nil)); !strings.Contains(s, "api.example.com") {
		t.Errorf("egress: %s", s)
	}
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "fakepay@1", "name": "main", "credentials": map[string]string{}})
	owner.must(409, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "fakepay@1", "name": "main", "credentials": map[string]string{}})
	owner.must(400, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "nope@1", "credentials": map[string]string{}})
	if n := len(owner.must(200, "GET", "/v1/connectors", nil)["connectors"].([]any)); n == 0 {
		t.Error("no connectors listed")
	}

	wf := publishFlow(t, owner, loanFlow)
	run := owner.must(201, "POST", "/v1/workflows/"+wf+"/runs", map[string]any{"input": map[string]any{"bvn": "22299999999", "amount": 5}})["run_id"].(string)
	owner.must(200, "POST", "/v1/pii/erase", map[string]any{"category": "bvn", "value": "22299999999"})
	owner.must(404, "POST", "/v1/pii/erase", map[string]any{"category": "bvn", "value": "22299999999"})
	if s := toJSON(owner.must(200, "GET", "/v1/runs/"+run+"?reveal=true", nil)); strings.Contains(s, "22299999999") {
		t.Error("erased BVN still readable")
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// raw returns a successful response's body as text (CSV and the like).
func (c *client) raw(t *testing.T, method, path string) string {
	t.Helper()
	status, out := c.do(method, path, nil)
	if status != 200 {
		t.Fatalf("%s %s: %d %v", method, path, status, out)
	}
	s, _ := out["raw"].(string)
	return s
}
