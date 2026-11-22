package api_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"

	payconn "github.com/israel-duff/taskiem/connectors/payments"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

// Collecting from several messages: "Add to the list" keeps a row on the
// person's own list across chats, once per message, checked as a payee
// is (staff add saved accounts only); each person sees their own; the
// list becomes a batch once every row has an amount; its rows wait with
// the batch, come back when it is cancelled, and leave once it is sent.
func TestCollectToPay(t *testing.T) {
	w := newWAWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.LoginBurst = 100
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = payRails{}
	for _, c := range []*connector.Connector{payconn.New(w.srv.PaymentsOps()), paystack.New(paystack.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	banks := []map[string]any{{"name": "Access Bank", "code": "044"}, {"name": "Guaranty Trust Bank", "code": "058"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner := w.tenant(t, "Ada Foods", "ada@collect.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "bayo", "title": "Bayo Rice Mills", "kind": "bank_account", "bank_name": "Access Bank", "account_number": "0123456789"})
	key := addPasskey(t, owner, "Phone")
	gtb := fitting(t, "058", "012345678")
	from := func(chat, msg string) map[string]any {
		return map[string]any{"kind": "link", "chat": chat, "message": msg}
	}

	// Three messages from two chats; the same message twice is refused.
	owner.must(201, "POST", "/v1/pay-collection", map[string]any{"payee": "bayo", "amount_kobo": 4500000, "purpose": "stock", "source": from("c1", "m1")})
	owner.must(201, "POST", "/v1/pay-collection", map[string]any{"account_number": gtb, "bank_name": "GTBank", "name": "Chidi Okeke", "source": from("c2", "m7")})
	if e := owner.must(409, "POST", "/v1/pay-collection", map[string]any{"payee": "bayo", "source": from("c1", "m1")}); e["code"] != "already" {
		t.Fatalf("twice: %v", e)
	}
	list := owner.must(200, "GET", "/v1/pay-collection", nil)
	rows := list["rows"].([]any)
	if list["count"] != float64(2) || list["no_amount"] != float64(1) || list["total_kobo"] != float64(4500000) {
		t.Fatalf("the list: %v", list)
	}
	second := rows[1].(map[string]any)
	if second["last4"] != gtb[6:] || second["bank_name"] != "GTBank" || second["name"] != "Chidi Okeke" || second["source"].(map[string]any)["message"] != "m7" {
		t.Fatalf("an account read from a message: %v", second)
	}
	if rows[0].(map[string]any)["payee"] != "bayo" {
		t.Fatalf("a saved account: %v", rows[0])
	}

	// Only those who may pay keep a list.
	staff := w.member(t, owner, "musa@collect.test", "admin")
	staff.must(403, "GET", "/v1/pay-collection", nil) // money.pay is the owner's unless given

	// Every row needs an amount first.
	if e := owner.must(400, "POST", "/v1/pay-collection/batch", map[string]any{}); e["code"] != "amount" {
		t.Fatalf("no amount: %v", e)
	}
	owner.must(200, "PATCH", "/v1/pay-collection/"+second["id"].(string), map[string]any{"amount_kobo": 2000000, "purpose": "wages"})

	// The list becomes a batch; its rows wait with it.
	b := owner.must(201, "POST", "/v1/pay-collection/batch", map[string]any{"title": "Friday"})["batch"].(map[string]any)
	if b["kind"] != "messages" || len(b["payments"].([]any)) != 2 {
		t.Fatalf("the batch: %v", b)
	}
	if got := owner.must(200, "GET", "/v1/pay-collection", nil); got["count"] != float64(0) || got["in_batch"] != b["id"] {
		t.Fatalf("waiting with the batch: %v", got)
	}
	owner.must(409, "POST", "/v1/pay-collection/batch", map[string]any{})

	// Cancelled: they come back.
	owner.must(200, "POST", "/v1/payment-batches/"+b["id"].(string)+"/cancel", nil)
	if got := owner.must(200, "GET", "/v1/pay-collection", nil); got["count"] != float64(2) || got["in_batch"] != nil {
		t.Fatalf("back after a cancel: %v", got)
	}

	// Sent: they leave the list.
	b2 := owner.must(201, "POST", "/v1/pay-collection/batch", map[string]any{})["batch"].(map[string]any)
	var ids []string
	for _, x := range b2["payments"].([]any) {
		ids = append(ids, x.(map[string]any)["id"].(string))
	}
	owner.must(202, "POST", "/v1/payment-batches/"+b2["id"].(string)+"/confirm", map[string]any{"payments": ids,
		"passkey": stepUp(t, owner, key, "money.pay.batch", "batch:"+b2["id"].(string))})
	if got := owner.must(200, "GET", "/v1/pay-collection", nil); got["count"] != float64(0) || got["in_batch"] != nil {
		t.Fatalf("gone once sent: %v", got)
	}

	// One off, and clearing.
	owner.must(201, "POST", "/v1/pay-collection", map[string]any{"payee": "bayo", "source": from("c1", "m9")})
	one := owner.must(201, "POST", "/v1/pay-collection", map[string]any{"payee": "bayo", "source": from("c3", "m1")})["rows"].([]any)[1].(map[string]any)
	if got := owner.must(200, "DELETE", "/v1/pay-collection/"+one["id"].(string), nil); got["count"] != float64(1) {
		t.Fatalf("one off: %v", got)
	}
	if got := owner.must(200, "DELETE", "/v1/pay-collection", nil); got["count"] != float64(0) {
		t.Fatalf("cleared: %v", got)
	}
}
