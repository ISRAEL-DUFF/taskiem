package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/iswallet"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/remote"
)

// fakeSubscriptions is iswallet's webhook subscriptions API (27 October
// 2026): endpoint_url and exact event types, a signing secret returned
// once, the deliveries log.
type fakeSubscriptions struct {
	mu      sync.Mutex
	n       int
	subs    map[string]map[string]any
	lastHit int // the status code the last delivery got, for the log
	srv     *httptest.Server
}

func newFakeSubscriptions(t *testing.T) *fakeSubscriptions {
	f := &fakeSubscriptions{subs: map[string]map[string]any{}, lastHit: 200}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSubscriptions) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", "req_fake")
	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/webhooks/subscriptions/")
	switch {
	case r.URL.Path == "/v1/providers":
		reply(200, map[string]any{"providers": []any{map[string]any{"code": "flutterwave", "currencies": []string{"NGN"}, "virtual_accounts": map[string]bool{"permanent": true}}}})
	case r.URL.Path == "/v1/webhooks/subscriptions" && r.Method == "POST":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		u, _ := url.Parse(body["endpoint_url"].(string))
		if u == nil || u.Scheme != "https" {
			reply(400, map[string]any{"error": map[string]any{"code": "INVALID_ENDPOINT_URL", "message": "must be https and public"}})
			return
		}
		f.n++
		sid := fmt.Sprintf("sub_%d", f.n)
		f.subs[sid] = map[string]any{"id": sid, "endpoint_url": body["endpoint_url"], "event_types": body["event_types"], "status": "active"}
		out := map[string]any{"signing_secret": "whsec_" + sid}
		for k, v := range f.subs[sid] {
			out[k] = v
		}
		reply(201, out)
	case r.URL.Path == "/v1/webhooks/subscriptions" && r.Method == "GET":
		list := []any{}
		for _, s := range f.subs {
			list = append(list, s)
		}
		reply(200, map[string]any{"subscriptions": list})
	case strings.HasSuffix(id, "/deliveries"):
		reply(200, map[string]any{"deliveries": []any{map[string]any{"event_type": "wallet.credit.posted", "status_code": f.lastHit,
			"response_body": map[bool]string{true: "", false: `{"error":"signature invalid"}`}[f.lastHit == 200], "attempted_at": "2026-10-27T10:00:00Z"}}})
	case f.subs[id] == nil:
		reply(404, map[string]any{"error": map[string]any{"code": "SUBSCRIPTION_NOT_FOUND", "message": "no such subscription"}})
	case r.Method == "GET":
		reply(200, f.subs[id])
	case r.Method == "PATCH":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.subs[id]["endpoint_url"], f.subs[id]["event_types"] = body["endpoint_url"], body["event_types"]
		reply(200, f.subs[id])
	case r.Method == "DELETE":
		delete(f.subs, id)
		w.WriteHeader(204)
	}
}

func (f *fakeSubscriptions) list() map[string]map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]map[string]any{}
	for k, v := range f.subs {
		out[k] = v
	}
	return out
}

// Connecting iswallet subscribes the connection to its three triggers
// with no pasting (docs/spec/iswallet-account-numbers.md I7); a signed
// credit to the registered URL reaches the money log; the connection page
// shows a failing delivery in iswallet's words; removing the connection
// removes the subscriptions.
func TestISWalletConnectionSubscribesItself(t *testing.T) {
	w := newWorld(t)
	f := newFakeSubscriptions(t)
	if err := w.env.Registry.Register(iswallet.New(iswallet.Options{BaseURL: f.srv.URL, TestURL: f.srv.URL})); err != nil {
		t.Fatal(err)
	}
	rec := &remote.Reconciler{Pool: w.env.DB.App, Vault: w.env.Vault, Registry: w.env.Registry, Egress: w.env.Egress,
		HooksURL: "https://hooks.taskiem.example/hooks", Check: time.Nanosecond}
	w.srv.Remote = rec
	owner := w.tenant(t, "Greenfield Academy", "bursar@greenfield.test")
	conn := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"api_key": "isw_good"}})

	subs := f.list()
	if len(subs) != 3 {
		t.Fatalf("subscriptions %v", subs)
	}
	var creditURL string
	for _, s := range subs {
		u, _ := url.Parse(s["endpoint_url"].(string))
		if u.Query().Get("env") != "prod" || u.Query().Get("connection") != "main" || u.Query().Get("subscription") == "" {
			t.Errorf("endpoint %v", u)
		}
		if strings.HasSuffix(u.Path, "/credit_event") {
			creditURL = s["endpoint_url"].(string)
			if got := fmt.Sprint(s["event_types"]); got != "[wallet.credit.posted wallet.credit.rejected wallet.credit.reversed]" {
				t.Errorf("credit events %s", got)
			}
		}
	}
	summary := func() map[string]any {
		for _, c := range owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
			if m := c.(map[string]any); m["id"] == conn["id"] {
				if hooks, _ := m["money_log_webhooks"].([]any); len(hooks) > 0 {
					t.Errorf("iswallet still asks for webhooks to be pasted: %v", hooks)
				}
				r, _ := m["remote"].(map[string]any)
				return r
			}
		}
		return nil
	}
	if r := summary(); r == nil || r["health"] != "ok" || r["subscriptions"].(float64) != 3 {
		t.Fatalf("connection %v", r)
	}

	// A credit iswallet signs with the subscription's secret, delivered to
	// the URL Taskiem registered, is recorded once in the money log.
	h := &ingest.Handler{Store: w.env.Store, Secrets: w.env.Vault, Connections: w.env.Vault, Registry: w.env.Registry,
		MoneyLog: &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", h))
	edge := httptest.NewServer(mux)
	t.Cleanup(edge.Close)
	u, _ := url.Parse(creditURL)
	sid := ""
	for k, s := range subs {
		if s["endpoint_url"] == creditURL {
			sid = k
		}
	}
	body := []byte(`{"id":"evt_c1","event_type":"wallet.credit.posted","wallet_id":"6f1e","occurred_at":"2026-10-27T09:14:03Z","data":{"txn_id":"t-2",
		"provider_reference":"100004261027091402123456","amount":5000000,"amount_credited":4930000,"currency":"NGN","occurred_at":"2026-10-27T09:14:02Z",
		"virtual_account":{"reference":"tsk_ab12cd34ef56gh78ij90kl12mn34op56","account_number":"9901234567","status":"active"}}}`)
	deliver := func(secret string) int {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(ts + "." + string(body)))
		req, _ := http.NewRequest("POST", edge.URL+u.Path+"?"+u.RawQuery, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-iSpend-Timestamp", ts)
		req.Header.Set("X-iSpend-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
		req.Header.Set("X-iSpend-Idempotency-Key", "wallet.credit.posted:t-2:6f1e:"+sid)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := deliver("whsec_wrong"); code != 401 {
		t.Errorf("a wrong signature: %d", code)
	}
	for range 2 {
		if code := deliver("whsec_" + sid); code != 200 && code != 202 {
			t.Fatalf("the credit: %d", code)
		}
	}
	var n int
	var amount int64
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*), COALESCE(max(amount_kobo), 0) FROM money_entries WHERE credit_id = '100004261027091402123456'`).Scan(&n, &amount); err != nil {
		t.Fatal(err)
	}
	if n != 1 || amount != 5000000 {
		t.Errorf("%d entries of %d kobo", n, amount)
	}

	// iswallet's log says our endpoint refused a delivery: the connection
	// says so in iswallet's words.
	f.mu.Lock()
	f.lastHit = 401
	f.mu.Unlock()
	if _, err := rec.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := summary()
	if r["health"] != "failing" || !strings.Contains(fmt.Sprint(r["problems"]), "got HTTP 401 from Taskiem") {
		t.Fatalf("failing %v", r)
	}

	// A key limited to another environment removes nothing, not even the
	// subscriptions.
	dev := keyFor(t, owner, "dev", "connection.manage")
	dev.must(403, "DELETE", "/v1/connections/"+conn["id"].(string)+"?force=true", nil)
	if left := f.list(); len(left) != 3 {
		t.Errorf("a refused delete released the subscriptions: %v", left)
	}

	// Removed with the connection.
	owner.must(204, "DELETE", "/v1/connections/"+conn["id"].(string), nil)
	if left := f.list(); len(left) != 0 {
		t.Errorf("subscriptions left %v", left)
	}
}

// A shared client's connection (decision 0031) holds no subscription: its
// events come through the partner's.
func TestISWalletSharedClientDoesNotSubscribe(t *testing.T) {
	w := newWorld(t)
	f := newFakeSubscriptions(t)
	if err := w.env.Registry.Register(iswallet.New(iswallet.Options{BaseURL: f.srv.URL, TestURL: f.srv.URL})); err != nil {
		t.Fatal(err)
	}
	w.srv.Remote = &remote.Reconciler{Pool: w.env.DB.App, Vault: w.env.Vault, Registry: w.env.Registry, Egress: w.env.Egress, HooksURL: "https://hooks.taskiem.example/hooks"}
	owner := w.tenant(t, "Sub Tenant", "owner@sub.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"api_key": "isw_shared", "shared_client": "true", "wallet_ids": "6f1e"}})
	if subs := f.list(); len(subs) != 0 {
		t.Errorf("subscriptions %v", subs)
	}
}
