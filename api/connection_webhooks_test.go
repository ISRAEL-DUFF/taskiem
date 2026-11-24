package api_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/moniepoint"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/connectors/prembly"
	"github.com/israel-duff/taskiem/engine/ingest"
)

// The money log hears a rail's events without a workflow (decision 0045):
// each prod connection of a rail lists the addresses to set in its
// dashboard, with what each carries, how it is checked and whether Taskiem
// also polls, and never a secret.
func TestConnectionMoneyLogWebhooks(t *testing.T) {
	w := newWorld(t)
	w.srv.HooksURL = "https://hooks.taskiem.test"
	if err := w.env.Registry.Register(paystack.New(paystack.Options{})); err != nil {
		t.Fatal(err)
	}
	if err := w.env.Registry.Register(moniepoint.New(moniepoint.Options{})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	const psKey, mpSecret = "sk_test_never_shown_1234", "mp-secret-never-shown"
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": psKey}})
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "dev", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": psKey}})
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "moniepoint@1", "name": "shop",
		"credentials": map[string]string{"api_key": "MK_TEST_ABC", "secret_key": mpSecret}})
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "fakepay@1", "name": "main", "credentials": map[string]string{}})

	out := owner.must(200, "GET", "/v1/connections", nil)
	if body := toJSON(out); strings.Contains(body, psKey) || strings.Contains(body, mpSecret) || strings.Contains(body, "MK_TEST_ABC") {
		t.Fatalf("a credential is in the list: %s", body)
	}
	me := owner.must(200, "GET", "/v1/me", nil)
	tenant, _ := me["tenant_id"].(string)
	hooks := map[string][]any{}
	for _, c := range out["connections"].([]any) {
		c := c.(map[string]any)
		key := c["connector"].(string) + "/" + c["environment"].(string)
		if h, ok := c["money_log_webhooks"].([]any); ok {
			hooks[key] = h
		} else {
			hooks[key] = nil
		}
	}
	if hooks["paystack/dev"] != nil || hooks["fakepay/prod"] != nil {
		t.Fatalf("a dev connection or a connector without events lists addresses: %s", toJSON(hooks))
	}

	ps := hooks["paystack/prod"]
	if len(ps) != 2 {
		t.Fatalf("paystack: %s", toJSON(ps))
	}
	charge, transfer := ps[0].(map[string]any), ps[1].(map[string]any)
	if charge["trigger"] != "charge_event" || transfer["trigger"] != "transfer_event" {
		t.Fatalf("paystack triggers: %s", toJSON(ps))
	}
	if got := toJSON(charge["events"]); got != `["payment.received","payment.returned","refund.failed","refund.pending","refund.processed","virtual_account.credited"]` {
		t.Fatalf("charge events: %s", got)
	}
	if got := toJSON(transfer["events"]); got != `["payout.failed","payout.reversed","payout.settled"]` {
		t.Fatalf("transfer events: %s", got)
	}
	want := "https://hooks.taskiem.test/" + tenant + "/connectors/paystack@1/charge_event?connection=main&env=prod"
	if tenant == "" || charge["url"] != want {
		t.Fatalf("charge url %v, want %s", charge["url"], want)
	}
	if charge["poll_minutes"] != float64(5) || transfer["poll_minutes"] != float64(5) {
		t.Fatalf("poll: %s", toJSON(ps))
	}
	if charge["title"] != "Payments you receive" || !strings.Contains(charge["where"].(string), "Live Webhook URL") {
		t.Fatalf("charge title/where: %s", toJSON(charge))
	}
	if v := charge["verification"].(string); !strings.Contains(v, "Paystack signs each delivery") || !strings.Contains(v, "Secret key") {
		t.Fatalf("verification: %q", v)
	}
	if _, ok := charge["placeholder"]; ok {
		t.Fatalf("a signed trigger has a placeholder: %s", toJSON(charge))
	}

	mp := hooks["moniepoint/prod"]
	if len(mp) != 1 {
		t.Fatalf("moniepoint: %s", toJSON(mp))
	}
	ev := mp[0].(map[string]any)
	if ev["trigger"] != "event" || ev["url"] != "https://hooks.taskiem.test/"+tenant+"/connectors/moniepoint@1/event?connection=shop&env=prod" {
		t.Fatalf("moniepoint: %s", toJSON(ev))
	}
	if got := toJSON(ev["events"]); got != `["payment.received","payout.failed","payout.reversed","payout.settled","virtual_account.credited"]` {
		t.Fatalf("moniepoint events: %s", got)
	}
	if _, polled := ev["poll_minutes"]; polled {
		t.Fatalf("moniepoint is not polled: %s", toJSON(ev))
	}
	if !strings.Contains(ev["where"].(string), "Webhook URLs") || !strings.Contains(ev["verification"].(string), "Moniepoint (Monnify) signs") {
		t.Fatalf("moniepoint where/verification: %s", toJSON(ev))
	}

	// Without a hooks URL, the public URL's /hooks.
	w.srv.HooksURL, w.srv.PublicURL = "", "https://app.taskiem.test"
	out = owner.must(200, "GET", "/v1/connections", nil)
	for _, c := range out["connections"].([]any) {
		c := c.(map[string]any)
		if c["connector"] != "moniepoint" {
			continue
		}
		got := c["money_log_webhooks"].([]any)[0].(map[string]any)["url"]
		if got != "https://app.taskiem.test/hooks/"+tenant+"/connectors/moniepoint@1/event?connection=shop&env=prod" {
			t.Fatalf("under the public URL: %v", got)
		}
	}
}

// Prembly signs webhooks with the business's public key, which proves
// nothing (F55), so its trigger takes a token Taskiem makes for each
// connection: random, never typed or set by anyone, shown once inside the
// webhook address when the connection is saved, and required on every
// delivery.
func TestPremblyWebhookTokenMadeByTaskiem(t *testing.T) {
	w := newWorld(t)
	if err := w.env.Registry.Register(prembly.New(prembly.Options{})); err != nil {
		t.Fatal(err)
	}
	h := &ingest.Handler{Store: w.env.Store, Secrets: w.env.Vault, Connections: w.env.Vault, Registry: w.env.Registry}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", h))
	edge := httptest.NewServer(mux)
	t.Cleanup(edge.Close)
	w.srv.HooksURL = edge.URL + "/hooks"
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	tenant, _ := owner.must(200, "GET", "/v1/me", nil)["tenant_id"].(string)

	// Nobody chooses the token.
	owner.must(400, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "prembly@1", "name": "main",
		"credentials": map[string]string{"api_key": "sk_test_prembly", "callback_token": "chosen-by-me"}})
	made := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "prembly@1", "name": "main",
		"credentials": map[string]string{"api_key": "sk_test_prembly"}})
	hooks, _ := made["webhooks"].([]any)
	if len(hooks) != 1 {
		t.Fatalf("webhooks: %s", toJSON(made))
	}
	hook := hooks[0].(map[string]any)
	addr, _ := hook["url"].(string)
	prefix := edge.URL + "/hooks/" + tenant + "/connectors/prembly@1/verification?connection=main&env=prod&token="
	tok := strings.TrimPrefix(addr, prefix)
	if hook["trigger"] != "verification" || !strings.HasPrefix(addr, prefix) || len(tok) != 64 || strings.Trim(tok, "0123456789abcdef") != "" {
		t.Fatalf("address %q: want %s and 64 hex characters", addr, prefix)
	}
	if !strings.Contains(hook["where"].(string), "Webhook URL") {
		t.Errorf("where: %v", hook["where"])
	}
	other := owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "prembly@1", "name": "branch",
		"credentials": map[string]string{"api_key": "sk_test_prembly"}})
	if strings.Contains(toJSON(other), tok) {
		t.Fatal("two connections share a token")
	}
	// Not shown again, and not settable or removable afterwards.
	if strings.Contains(toJSON(owner.must(200, "GET", "/v1/connections", nil)), tok) {
		t.Fatal("the token is in the connection list")
	}
	id := made["id"].(string)
	owner.must(400, "PUT", "/v1/connections/"+id+"/credentials", map[string]any{"credentials": map[string]string{"callback_token": "chosen-by-me"}})
	owner.must(400, "PUT", "/v1/connections/"+id+"/credentials", map[string]any{"credentials": map[string]string{"callback_token": ""}})

	// A delivery signed with the public key and no token is refused; the
	// address shown is accepted.
	body := []byte(`{"status":true,"response_code":"00","data":{"widget_info":{"user_ref":"u1"}},"verification":{"status":"VERIFIED","reference":"r1"}}`)
	m := hmac.New(sha256.New, []byte("pk_test_prembly"))
	m.Write(body)
	deliver := func(to string) int {
		req, _ := http.NewRequest("POST", to, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-prembly-signature", base64.StdEncoding.EncodeToString(m.Sum(nil)))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if st := deliver(strings.TrimSuffix(prefix, "&token=")); st != 401 {
		t.Errorf("no token: %d, want 401", st)
	}
	if st := deliver(prefix + strings.Repeat("0", 64)); st != 401 {
		t.Errorf("wrong token: %d, want 401", st)
	}
	if st := deliver(addr); st != 202 {
		t.Errorf("the address shown: %d, want 202", st)
	}
}
