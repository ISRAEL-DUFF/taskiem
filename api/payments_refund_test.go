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

// Pay it back (H7 review): a payment that pays back an order's refund
// names the order; it is a refund, no more than the order owes, one on its
// way or paid at a time, and once paid the order is refunded.
func TestPayItBack(t *testing.T) {
	ctx := context.Background()
	w, owner := newDispatchWorld(t, "Ada Kitchen")
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.LoginBurst = 100
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test",
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	w.srv.AccountRails = &standInRails{balance: 10_000_000, quotes: true, names: map[string]string{}}
	for _, c := range []*connector.Connector{payconn.New(w.srv.PaymentsOps()), paystack.New(paystack.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	banks := []map[string]any{{"name": "Guaranty Trust Bank", "code": "058"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	key := addPasskey(t, owner, "Phone")
	ref := w.order(t, "none")
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE storefront_orders SET status = 'refund_due', paid_kobo = 650000 WHERE reference = $1`, ref); err != nil {
		t.Fatal(err)
	}
	acct := fitting(t, "058", "012345678")
	body := func(kobo int64) map[string]any {
		return map[string]any{"account_number": acct, "bank_name": "Guaranty Trust Bank", "amount_kobo": kobo, "purpose": "other", "order_reference": ref}
	}
	if e := owner.must(400, "POST", "/v1/payments", body(700_000)); e["code"] != "over_owed" {
		t.Fatalf("more than owed: %v", e)
	}
	owner.must(404, "POST", "/v1/payments", map[string]any{"account_number": acct, "bank_name": "Guaranty Trust Bank", "amount_kobo": 1000, "order_reference": "TK-NOPE"})
	x := owner.must(201, "POST", "/v1/payments", body(650_000))["payment"].(map[string]any)
	if x["purpose"] != "refund" || x["refund_order"] != ref {
		t.Fatalf("a refund paid back: %v", x)
	}
	second := owner.must(201, "POST", "/v1/payments", body(650_000))["payment"].(map[string]any)
	id := x["id"].(string)
	sent := owner.must(202, "POST", "/v1/payments/"+id+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+id)})["payment"].(map[string]any)
	// One on its way: no other is sent, nor prepared.
	sid := second["id"].(string)
	if e := owner.must(409, "POST", "/v1/payments/"+sid+"/confirm", map[string]any{"again": true, "passkey": stepUp(t, owner, key, "money.pay", "payment:"+sid)}); e["code"] != "refund_on_its_way" {
		t.Fatalf("a second refund confirmed: %v", e)
	}
	if e := owner.must(409, "POST", "/v1/payments", body(650_000)); e["code"] != "refund_on_its_way" {
		t.Fatalf("a second refund prepared: %v", e)
	}
	if _, err := w.srv.PaymentsOps().Finish(ctx, w.biz, uuid.MustParse(sent["run_id"].(string)), uuid.MustParse(id), "paid", "BOLA ADE", "po_refund", "", ptr(int64(2500)), "bank"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT status FROM storefront_orders WHERE reference = $1`, ref).Scan(&status); err != nil || status != "refunded" {
		t.Fatalf("the order once paid back: %q %v", status, err)
	}
	// Refunded: nothing more is owed.
	if e := owner.must(409, "POST", "/v1/payments", body(1000)); e["code"] != "not_owed" {
		t.Fatalf("a refunded order: %v", e)
	}
}
