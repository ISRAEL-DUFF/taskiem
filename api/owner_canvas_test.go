package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/connectors/whatsapp"
)

// fakePaystackBanks is Paystack's banks, account names and recipients.
func fakePaystackBanks(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/bank":
			_, _ = w.Write([]byte(`{"status":true,"data":[{"name":"Guaranty Trust Bank","code":"058"},{"name":"Zenith Bank","code":"057"}]}`))
		case r.URL.Path == "/bank/resolve" && r.URL.Query().Get("account_number") == "0123456789":
			_, _ = w.Write([]byte(`{"status":true,"data":{"account_number":"0123456789","account_name":"BOLA ADE"}}`))
		case r.URL.Path == "/bank/resolve":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"status":false,"message":"Could not resolve account name."}`))
		case r.URL.Path == "/transferrecipient" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"status":true,"data":{"recipient_code":"RCP_2x5j67tnnw1t98k","name":"BOLA ADE"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Templates and the canvas for everyone (no-technical-steps N8): people
// are paid by bank and account number with the name confirmed, WhatsApp
// templates are chosen by what they say, the Advanced switch, and a
// workflow made only from the canvas's plain pieces publishes.
func TestOwnerTemplatesAndCanvas(t *testing.T) {
	w := newWorld(t)
	if err := w.env.Registry.Register(paystack.New(paystack.Options{BaseURL: fakePaystackBanks(t).URL})); err != nil {
		t.Fatal(err)
	}
	graph := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/1029384756/message_templates" {
			_, _ = rw.Write([]byte(`{"data":[{"name":"payment_reminder","language":"en","category":"UTILITY","status":"APPROVED","components":[{"type":"BODY","text":"Hello {{1}}, you owe {{2}}."}]}]}`))
			return
		}
		rw.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(graph.Close)
	if err := w.env.Registry.Register(whatsapp.New(whatsapp.Options{BaseURL: graph.URL})); err != nil {
		t.Fatal(err)
	}
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")

	// Paying someone: nothing to ask with until Paystack is connected.
	owner.must(409, "GET", "/v1/paystack/banks", nil)
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "credentials": map[string]string{"secret_key": "sk_test_good"}})
	banks := owner.must(200, "GET", "/v1/paystack/banks", nil)["banks"].([]any)
	if len(banks) != 2 || banks[0].(map[string]any)["name"] != "Guaranty Trust Bank" {
		t.Fatalf("banks: %v", banks)
	}
	if got := owner.must(200, "POST", "/v1/paystack/resolve", map[string]any{"bank_code": "058", "account_number": "012 345 6789"}); got["account_name"] != "BOLA ADE" {
		t.Fatalf("the name on the account: %v", got)
	}
	if e := owner.must(400, "POST", "/v1/paystack/resolve", map[string]any{"bank_code": "058", "account_number": "9999999999"})["error"].(string); !strings.Contains(e, "no account with that number") {
		t.Errorf("a wrong number: %q", e)
	}
	if e := owner.must(400, "POST", "/v1/paystack/resolve", map[string]any{"bank_code": "058", "account_number": "12345"})["error"].(string); !strings.Contains(e, "10 digits") {
		t.Errorf("a short number: %q", e)
	}
	rcp := owner.must(200, "POST", "/v1/paystack/recipients", map[string]any{"bank_code": "058", "account_number": "0123456789", "name": "BOLA ADE"})
	if rcp["recipient_code"] != "RCP_2x5j67tnnw1t98k" {
		t.Fatalf("recipient: %v", rcp)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit?limit=50", nil))
	if !strings.Contains(audit, `"paystack.recipient"`) || !strings.Contains(audit, `"account_last4":"6789"`) || strings.Contains(audit, "0123456789") {
		t.Errorf("the audit: %s", audit)
	}

	// A WhatsApp template by what it says: the connection needs its
	// business account first.
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "whatsapp@1",
		"credentials": map[string]string{"access_token": "EAAtest", "phone_number_id": "106540352242922"}})
	if e := owner.must(409, "GET", "/v1/whatsapp/templates", nil)["error"].(string); !strings.Contains(e, "WhatsApp Business Account ID") {
		t.Errorf("without the account: %q", e)
	}
	conns := owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any)
	for _, c := range conns {
		if m := c.(map[string]any); m["connector"] == "whatsapp" {
			owner.must(204, "PUT", "/v1/connections/"+m["id"].(string)+"/credentials", map[string]any{"credentials": map[string]string{"business_account_id": "1029384756"}})
		}
	}
	ts := owner.must(200, "GET", "/v1/whatsapp/templates", nil)["templates"].([]any)
	if len(ts) != 1 || ts[0].(map[string]any)["body"] != "Hello {{1}}, you owe {{2}}." || ts[0].(map[string]any)["variables"] != float64(2) {
		t.Fatalf("templates: %v", ts)
	}

	// The Advanced switch: off for a business on no plan, on when an owner
	// turns it on, and not for a builder to change.
	if owner.must(200, "GET", "/v1/me", nil)["advanced"] != false {
		t.Error("advanced is on by default")
	}
	builder := addMember(t, w, owner, "bo", "builder")
	builder.must(403, "PUT", "/v1/settings/advanced", map[string]any{"advanced": true})
	owner.must(200, "PUT", "/v1/settings/advanced", map[string]any{"advanced": true})
	if builder.must(200, "GET", "/v1/me", nil)["advanced"] != true || builder.must(200, "GET", "/v1/settings/advanced", nil)["chosen"] != true {
		t.Error("advanced is not on for the business")
	}

	// A workflow made with the canvas's plain pieces alone (the schedule
	// picker, a value from an earlier step, a rule): web/src/lib/plaincanvas
	// makes exactly this definition (plaincanvas.test.ts).
	wf := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "Friday check", "definition": map[string]any{
		"schema": "wd/v1", "id": "wf_fridayCheck", "version": 1, "name": "Friday check",
		"trigger": map[string]any{"type": "schedule", "config": map[string]any{"cron": "0 10 * * 5", "timezone": "Africa/Lagos"}},
		"steps": []any{
			map[string]any{"id": "approve", "type": "approval", "name": "Get it approved", "config": map[string]any{"role": "owner", "timeout": "24h", "subject": map[string]any{"week": "this week"}}},
			map[string]any{"id": "note", "type": "transform", "name": "Note the decision", "needs": []string{"approve"},
				"when":   "=steps.approve.output.decision == 'approved'",
				"config": map[string]any{"output": map[string]any{"decision": "=steps.approve.output.decision"}}},
		}}})
	pub := owner.must(200, "POST", "/v1/workflows/"+wf["id"].(string)+"/versions/1/publish", nil)
	if pub["state"] != "published" {
		t.Fatalf("publish: %v", pub)
	}
}
