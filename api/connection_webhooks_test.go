package api_test

import (
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/moniepoint"
	"github.com/israel-duff/taskiem/connectors/paystack"
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
	if got := toJSON(charge["events"]); got != `["payment.received","payment.returned","refund.failed","refund.pending","refund.processed"]` {
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
