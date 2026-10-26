package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
)

const collectByTransfer = `{"schema":"wd/v1","id":"wf_transfer","version":1,"name":"collect","trigger":{"type":"manual"},"steps":[
  {"id":"account","type":"capability","uses":"collect_transfer","connection":"main",
   "input":{"amount_kobo":500000,"customer":{"name":"Ada Obi","email":"ada@example.com"},"description":"Deposit"}}]}`

// A workflow that collects by transfer on Paystack is told, without
// being blocked, that Paystack must turn Pay with Transfer on for the
// business; the connection says the same; both stop once a transfer
// account has been opened on the connection.
func TestReadinessPaystackTransfer(t *testing.T) {
	w := newWorld(t)
	if err := w.env.Registry.Register(paystack.New(paystack.Options{})); err != nil {
		t.Fatal(err)
	}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	owner := w.tenant(t, "Ada Stores", "ada@ada.test")
	id := owner.must(201, "POST", "/v1/workflows", map[string]any{"name": "collect", "definition": json.RawMessage(collectByTransfer)})["id"].(string)
	const want = "Paystack must turn on Pay with Transfer for this business: ask Paystack support."

	// Not connected: the connection is the need, and the note is not.
	r := owner.must(200, "GET", "/v1/workflows/"+id+"/versions/1/readiness", nil)
	if got := needsOf(t, r["needs"]); got["connection/main/prod"] == nil || got["connection/main/prod"]["problem"] != "missing" || strings.Contains(toJSON(r), "support") {
		t.Fatalf("before connecting: %s", toJSON(r))
	}
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	r = owner.must(200, "GET", "/v1/workflows/"+id+"/versions/1/readiness", nil)
	n := needsOf(t, r["needs"])["connection/main/prod"]
	if r["ready"] != true || n == nil || n["problem"] != "support" || n["blocking"] != false || n["connector"] != "paystack" || n["service"] != "Paystack" ||
		n["capability"] != "collect_transfer" || !strings.HasPrefix(n["message"].(string), want) || n["steps"].([]any)[0].(map[string]any)["id"] != "account" {
		t.Fatalf("the note: %v (all %s)", n, toJSON(r))
	}
	// It does not block publishing, and a draft is told the same.
	owner.must(200, "POST", "/v1/workflows/"+id+"/versions/1/publish", nil)
	if d := owner.must(200, "POST", "/v1/readiness", map[string]any{"definition": json.RawMessage(collectByTransfer)}); !strings.Contains(toJSON(d), `"problem":"support"`) {
		t.Errorf("draft: %s", toJSON(d))
	}
	// The connection says so too.
	conns := owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any)
	if len(conns) != 1 || !strings.HasPrefix(toJSON(conns[0].(map[string]any)["notes"]), `["`+want) {
		t.Fatalf("connection notes: %s", toJSON(conns))
	}

	// A transfer account opened on the connection: the note goes.
	x := owner.must(201, "POST", "/v1/invoices", map[string]any{"customer": map[string]any{"name": "Ada Obi", "email": "ada@example.com"}, "amount_kobo": 500000})
	tenant := tenantIDNamed(t, w, "Ada Stores")
	if _, err := w.env.DB.Admin.Exec(context.Background(), `INSERT INTO customer_invoice_accounts (id, tenant_id, invoice_id, seq, kind, connector, connection, payment_id, amount_kobo, status, account_number, bank_name)
		VALUES ($1, $2, $3, 1, 'transfer', 'paystack', 'main', 'tsk_pay1', 500000, 'open', '9900112233', 'Titan')`, uuid.New(), tenant, x["id"]); err != nil {
		t.Fatal(err)
	}
	r = owner.must(200, "GET", "/v1/workflows/"+id+"/versions/1/readiness", nil)
	if strings.Contains(toJSON(r), "support") {
		t.Errorf("after a transfer: %s", toJSON(r))
	}
	conns = owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any)
	if _, ok := conns[0].(map[string]any)["notes"]; ok {
		t.Errorf("connection still noted: %s", toJSON(conns))
	}
}
