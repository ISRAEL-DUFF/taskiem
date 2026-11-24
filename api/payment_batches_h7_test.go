package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	payconn "github.com/israel-duff/taskiem/connectors/payments"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

// Paying several (H7, g-pay-batch): the foot says the fees and the
// balance; when the balance is short it says by how much and which rows
// fit, and confirming is refused; with enough, the batch goes and has one
// diary line in the chat it was paid from.
func TestPaySeveralFoot(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.LoginBurst = 100
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	rails := &standInRails{balance: 2_500_000, quotes: true, names: map[string]string{}}
	w.srv.AccountRails = rails
	for _, c := range []*connector.Connector{payconn.New(w.srv.PaymentsOps()), paystack.New(paystack.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	banks := []map[string]any{{"name": "Access Bank", "code": "044"}, {"name": "Guaranty Trust Bank", "code": "058"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner := w.tenant(t, "Ada Foods", "ada@foot.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	for _, s := range []map[string]any{
		{"name": "bayo", "title": "Bayo Rice Mills", "kind": "bank_account", "bank_name": "Access Bank", "account_number": "0123456789"},
		{"name": "chidi", "title": "Chidi Okeke", "kind": "bank_account", "bank_name": "Guaranty Trust Bank", "account_number": "0987654321"},
	} {
		owner.must(201, "POST", "/v1/saved", s)
	}
	key := addPasskey(t, owner, "Phone")
	b := owner.must(201, "POST", "/v1/payment-batches", map[string]any{"title": "riders, week 45", "where": "taskiem", "rows": []map[string]any{
		{"payee": "bayo", "amount_kobo": 1_000_000, "purpose": "delivery"},
		{"payee": "chidi", "amount_kobo": 2_000_000, "purpose": "delivery"},
	}})["batch"].(map[string]any)
	id := b["id"].(string)
	pays := b["payments"].([]any)
	first := pays[0].(map[string]any)["id"]
	if b["fees_kobo"] != float64(5000) || b["fees_unknown"] != float64(0) || b["balance_kobo"] != float64(2_500_000) || b["short_kobo"] != float64(505_000) ||
		len(b["fit"].([]any)) != 1 || b["fit"].([]any)[0] != first {
		t.Fatalf("the foot: %v", b)
	}
	rows := []any{first, pays[1].(map[string]any)["id"]}
	if e := owner.must(409, "POST", "/v1/payment-batches/"+id+"/confirm", map[string]any{"payments": rows, "passkey": stepUp(t, owner, key, "money.pay.batch", "batch:"+id)}); e["code"] != "short" ||
		!strings.Contains(e["error"].(string), "₦5,050 short for all 2. The first 1 fit") {
		t.Fatalf("short: %v", e)
	}
	rails.mu.Lock()
	rails.balance = 10_000_000
	rails.mu.Unlock()
	if got := owner.must(200, "GET", "/v1/payment-batches/"+id, nil)["batch"].(map[string]any); got["short_kobo"] != float64(0) || len(got["fit"].([]any)) != 2 {
		t.Fatalf("enough: %v", got)
	}
	owner.must(202, "POST", "/v1/payment-batches/"+id+"/confirm", map[string]any{"payments": rows, "passkey": stepUp(t, owner, key, "money.pay.batch", "batch:"+id)})
	var lines int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM chat_history WHERE batch_id = $1 AND kind = 'diary'`, id).Scan(&lines); err != nil || lines != 1 {
		t.Fatalf("one diary line: %d %v", lines, err)
	}
	found := false
	for _, m := range owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
		if mm := m.(map[string]any); mm["batch"] == id && mm["text"] == "Paid 2 people ₦30,000 (riders, week 45)" {
			found = true
		}
	}
	if !found {
		t.Fatal("the batch's diary line")
	}
}
