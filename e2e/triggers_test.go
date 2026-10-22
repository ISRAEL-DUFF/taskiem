package e2e

import (
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/api"
	"github.com/israel-duff/taskiem/connectors/builtin"
	"github.com/israel-duff/taskiem/engine/ingest"
	rt "github.com/israel-duff/taskiem/engine/runtime/runtimetest"
)

// stack is the API and the edge on one test server, over the harness engine.
type stack struct {
	*rt.Env
	url string
}

func newStack(t *testing.T, opts builtin.Options) *stack {
	e := rt.New(t)
	if err := builtin.Register(e.Registry, opts); err != nil {
		t.Fatal(err)
	}
	hooks := &ingest.Handler{Store: e.Store, Secrets: e.Vault, Connections: e.Vault, Registry: e.Registry}
	srv := httptest.NewServer((&api.Server{Store: e.Store, Vault: e.Vault, Registry: e.Registry, AllowSignup: true, Ingest: hooks}).Handler())
	t.Cleanup(srv.Close)
	return &stack{Env: e, url: srv.URL}
}

// call makes an API or webhook request and decodes the JSON reply.
func (s *stack) call(t *testing.T, token, method, path string, body any, hdr ...string) (int, map[string]any) {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case []byte:
		raw = b
	default:
		raw, _ = json.Marshal(b)
	}
	req, _ := http.NewRequest(method, s.url+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func (s *stack) must(t *testing.T, want int, token, method, path string, body any, hdr ...string) map[string]any {
	t.Helper()
	got, out := s.call(t, token, method, path, body, hdr...)
	if got != want {
		t.Fatalf("%s %s: %d, want %d: %v", method, path, got, want, out)
	}
	return out
}

func (s *stack) login(t *testing.T, email string) string {
	return s.must(t, 200, "", "POST", "/v1/auth/login", map[string]any{"email": email, "password": "correct horse battery", "bearer": true})["token"].(string)
}

// secret stores a prod secret through the API, which readiness checks
// before a publish, and gives the harness worker the same value.
func (s *stack) secret(t *testing.T, owner, name, value string) {
	t.Helper()
	s.must(t, 204, owner, "PUT", "/v1/secrets/prod/"+name, map[string]any{"value": value})
	s.Secrets[name] = value
}

// publishFile creates and publishes a dogfood workflow through the API and
// returns its id and its trigger URL in prod.
func (s *stack) publishFile(t *testing.T, owner, file string) (string, string) {
	t.Helper()
	doc, err := os.ReadFile("../flows/dogfood/" + file)
	if err != nil {
		t.Fatal(err)
	}
	wf := s.must(t, 201, owner, "POST", "/v1/workflows", map[string]any{"name": file, "definition": json.RawMessage(doc)})["id"].(string)
	s.must(t, 200, owner, "POST", "/v1/workflows/"+wf+"/versions/1/publish", nil)
	for _, tr := range s.must(t, 200, owner, "GET", "/v1/workflows/"+wf+"/triggers", nil)["triggers"].([]any) {
		m := tr.(map[string]any)
		if m["environment"] == "prod" {
			u, _ := m["url"].(string)
			return wf, u
		}
	}
	return wf, ""
}

func mac(h func() hash.Hash, key string, body []byte) string {
	m := hmac.New(h, []byte(key))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func TestOpsPayoutFailureAlertEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var sms, slack []string
	termii := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		sms = append(sms, in["to"].(string)+": "+in["sms"].(string))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"message_id":"m1","message":"Successfully Sent","balance":100}`))
	}))
	defer termii.Close()
	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		slack = append(slack, in["text"].(string))
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer slackSrv.Close()

	s := newStack(t, builtin.Options{TermiiURL: termii.URL})
	tenant := s.must(t, 201, "", "POST", "/v1/signup", map[string]any{"tenant": "Holdco Ops", "email": "oncall@holdco.test", "password": "correct horse battery"})["tenant_id"].(string)
	owner := s.login(t, "oncall@holdco.test")
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "credentials": map[string]string{"api_key": "isw_ops", "webhook_secret": "whsec_ops"}})
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "termii@1", "credentials": map[string]string{"api_key": "tm_key", "sender_id": "Holdco"}})
	s.must(t, 204, owner, "PUT", "/v1/variables/prod/ops_on_call_phone", map[string]any{"value": "2348000000001"})
	s.must(t, 204, owner, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "127.0.0.1"})
	s.secret(t, owner, "ops_slack_webhook_url", slackSrv.URL+"/services/T/B/X")
	_, hook := s.publishFile(t, owner, "ops-payout-failure-alert.wd.json")
	if hook != "/hooks/"+tenant+"/connectors/iswallet@1/outflow_event?env=prod" {
		t.Fatalf("hook %q", hook)
	}

	isw := newFakeIswallet()
	send := func(o map[string]any, eventType string) map[string]any {
		body, hdr := isw.event(o, eventType, "whsec_ops")
		return s.must(t, 202, "", "POST", hook, body, hdr...)
	}
	payout := map[string]any{"outflow_id": "out_9", "operation_id": "out_9", "wallet_id": "w_company", "amount": 250000, "fee": 5350, "currency": "NGN", "idempotency_key": "tsk_x", "provider_reference": "tsk_x"}
	if n := len(send(payout, "wallet.outflow.failed")["runs"].([]any)); n != 1 {
		t.Fatalf("failed payout started %d runs", n)
	}
	send(payout, "wallet.outflow.failed") // iswallet redelivers
	if n := len(send(payout, "wallet.outflow.confirmed")["runs"].([]any)); n != 0 {
		t.Errorf("confirmed payout started %d alert runs", n)
	}
	rt.WaitFor(t, 10*time.Second, "alerts", func() bool {
		s.Drain(t)
		mu.Lock()
		defer mu.Unlock()
		return len(sms) >= 1 && len(slack) >= 1
	})
	mu.Lock()
	defer mu.Unlock()
	want := "iswallet payout failed: 2500 NGN, outflow out_9, wallet w_company (beneficiary account closed)"
	if len(sms) != 1 || sms[0] != "2348000000001: "+want || len(slack) != 1 || slack[0] != want {
		t.Errorf("sms %v, slack %v", sms, slack)
	}
}

func TestISpendCreditExceptionsEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var notices []map[string]any
	var keys []string
	var slack []string
	ispend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		if r.URL.Path == "/internal/wallet-credit-exceptions" && r.Header.Get("Authorization") == "Bearer isp_token" {
			notices = append(notices, in)
			keys = append(keys, r.Header.Get("Idempotency-Key"))
		}
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ispend.Close()
	slackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		slack = append(slack, in["text"].(string))
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	defer slackSrv.Close()

	s := newStack(t, builtin.Options{})
	tenant := s.must(t, 201, "", "POST", "/v1/signup", map[string]any{"tenant": "iSpend", "email": "ops@ispend.test", "password": "correct horse battery"})["tenant_id"].(string)
	owner := s.login(t, "ops@ispend.test")
	s.must(t, 201, owner, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "ispend",
		"credentials": map[string]string{"api_key": "isw_ispend", "webhook_secret": "whsec_ispend"}})
	s.must(t, 204, owner, "PUT", "/v1/variables/prod/ispend_api_url", map[string]any{"value": ispend.URL})
	s.must(t, 204, owner, "POST", "/v1/egress", map[string]any{"environment": "prod", "host": "127.0.0.1"})
	s.secret(t, owner, "ispend_api_token", "isp_token")
	s.secret(t, owner, "ops_slack_webhook_url", slackSrv.URL+"/services/T/B/X")
	_, hook := s.publishFile(t, owner, "ispend-credit-exceptions.wd.json")
	if hook != "/hooks/"+tenant+"/connectors/iswallet@1/credit_event?env=prod" {
		t.Fatalf("hook %q", hook)
	}
	hook += "&connection=ispend"
	post := func(id, typ string, data map[string]any) map[string]any {
		body, hdr := creditEvent(id, typ, "w_cust_7", data, "whsec_ispend")
		return s.must(t, 202, "", "POST", hook, body, hdr...)
	}
	if n := len(post("evt_rej", "wallet.credit.rejected", map[string]any{"amount": 1500000, "source_ref": "VA-44"})["runs"].([]any)); n != 1 {
		t.Fatalf("rejected credit started %d runs", n)
	}
	post("evt_rej", "wallet.credit.rejected", map[string]any{"amount": 1500000, "source_ref": "VA-44"}) // redelivery
	post("evt_rev", "wallet.credit.reversed", map[string]any{"txn_id": "txn_9", "reversal_reference": "RV-1"})
	if n := len(post("evt_ok", "wallet.credit.posted", map[string]any{"amount": 500000, "txn_id": "txn_10"})["runs"].([]any)); n != 0 {
		t.Errorf("a normal credit started %d runs", n)
	}
	rt.WaitFor(t, 15*time.Second, "notices", func() bool {
		s.Drain(t)
		mu.Lock()
		defer mu.Unlock()
		return len(notices) >= 2 && len(slack) >= 2
	})
	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 2 || len(slack) != 2 {
		t.Fatalf("notices %v, slack %v", notices, slack)
	}
	byKind := map[string]map[string]any{}
	for _, n := range notices {
		byKind[n["kind"].(string)] = n
	}
	if r := byKind["rejected"]; r == nil || r["amount_kobo"] != float64(1500000) || r["event_id"] != "evt_rej" || r["wallet_id"] != "w_cust_7" {
		t.Errorf("rejected notice %v", byKind["rejected"])
	}
	if r := byKind["reversed"]; r == nil || r["amount_kobo"] != float64(0) || r["iswallet"].(map[string]any)["reversal_reference"] != "RV-1" {
		t.Errorf("reversed notice %v", byKind["reversed"])
	}
	if keys[0] == "" || keys[0] == keys[1] {
		t.Errorf("idempotency keys %v", keys)
	}
	if !strings.Contains(strings.Join(slack, "|"), "iSpend top-up rejected and refunded to the sender: wallet w_cust_7, 15000 NGN, event evt_rej") {
		t.Errorf("slack %v", slack)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
