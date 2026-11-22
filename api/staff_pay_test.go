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

// A staff member's account for payments outside payroll (payroll is
// Payrolla's): an owner links one of the business's saved bank accounts
// to them with a passkey; their page shows it masked; Pay on them goes
// there and records them; linked to another account, the next payment is
// said changed; unlinked, they cannot be paid; a batch row pays them too.
func TestPayStaff(t *testing.T) {
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
	owner := w.tenant(t, "Ada Foods", "ada@staffpay.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	musaAcct := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "musa", "title": "Musa Bello", "kind": "bank_account",
		"bank_name": "Access Bank", "account_number": "0123456789"})["saved"].(map[string]any)
	other := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "musa-gtb", "title": "Musa Bello", "kind": "bank_account",
		"bank_name": "Guaranty Trust Bank", "account_number": "0987654321"})["saved"].(map[string]any)
	note := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "wifi", "title": "Wifi", "kind": "text", "details": map[string]any{"body": "1234"}})["saved"].(map[string]any)
	key := addPasskey(t, owner, "Phone")
	musa := w.member(t, owner, "musa@staffpay.test", "admin")
	musaID := idOf(t, musa)
	path := "/v1/staff/" + musaID + "/pay-account"

	// Not linked: no account, and no payment.
	if got := owner.must(200, "GET", "/v1/staff/"+musaID, nil); got["pay_account"] != nil {
		t.Fatalf("linked already: %v", got["pay_account"])
	}
	if e := owner.must(409, "POST", "/v1/payments", map[string]any{"staff_id": musaID, "amount_kobo": 500000, "purpose": "wages"}); e["code"] != "no_bank" {
		t.Fatalf("not linked: %v", e)
	}

	// Linked by an owner with a passkey for this staff member; not by
	// another admin; only a saved bank account.
	musa.must(403, "PUT", path, map[string]any{"saved_id": musaAcct["id"]})
	owner.must(400, "PUT", path, map[string]any{"saved_id": note["id"]})
	owner.must(404, "PUT", path, map[string]any{"saved_id": uuid.NewString()})
	if e := owner.must(403, "PUT", path, map[string]any{"saved_id": musaAcct["id"]}); e["step_up"] == nil {
		t.Fatalf("no passkey: %v", e)
	}
	linked := owner.must(200, "PUT", path, map[string]any{"saved_id": musaAcct["id"], "passkey": stepUp(t, owner, key, "staff.account", musaID)})["pay_account"].(map[string]any)
	if linked["name"] != "musa" || linked["last4"] != "6789" || linked["bank_name"] != "Access Bank" {
		t.Fatalf("linked: %v", linked)
	}
	if got := owner.must(200, "GET", "/v1/staff/"+musaID, nil)["pay_account"].(map[string]any); got["saved_id"] != musaAcct["id"] || got["linked_by_name"] == "" {
		t.Fatalf("on their page: %v", got)
	}
	// Those who may pay see the staff with where they are paid.
	found := false
	for _, x := range owner.must(200, "GET", "/v1/payments/staff", nil)["staff"].([]any) {
		if m := x.(map[string]any); m["user_id"] == musaID && m["pay_account"] != nil {
			found = true
		}
	}
	if !found {
		t.Errorf("not in the payers' staff list")
	}

	// Pay on them: to the linked account, recorded as theirs.
	p := owner.must(201, "POST", "/v1/payments", map[string]any{"staff_id": musaID, "amount_kobo": 500000, "purpose": "wages", "note": "Advance"})["payment"].(map[string]any)
	if p["staff_id"] != musaID || p["saved_id"] != musaAcct["id"] || p["account"] != "••6789" || p["payee"] != "Musa Bello" {
		t.Fatalf("paid: %v", p)
	}
	owner.must(202, "POST", "/v1/payments/"+p["id"].(string)+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+p["id"].(string))})
	owner.must(400, "POST", "/v1/payments", map[string]any{"staff_id": musaID, "payee": "musa", "amount_kobo": 500000})

	// Linked to another account: the next payment to them is said changed.
	owner.must(200, "PUT", path, map[string]any{"saved_id": other["id"], "passkey": stepUp(t, owner, key, "staff.account", musaID)})
	if q := owner.must(201, "POST", "/v1/payments", map[string]any{"staff_id": musaID, "amount_kobo": 500000, "purpose": "wages"})["payment"].(map[string]any); q["changed_account"] != true || q["account"] != "••4321" {
		t.Fatalf("a changed account: %v", q)
	}

	// In a batch.
	b := owner.must(201, "POST", "/v1/payment-batches", map[string]any{"rows": []map[string]any{{"staff_id": musaID, "amount_kobo": 300000, "purpose": "wages"}}})["batch"].(map[string]any)
	if b["count"] != float64(1) || b["payments"].([]any)[0].(map[string]any)["staff_id"] != musaID {
		t.Fatalf("a batch: %v", b)
	}

	// Unlinked: they are not paid.
	owner.must(200, "PUT", path, map[string]any{"saved_id": nil, "passkey": stepUp(t, owner, key, "staff.account", musaID)})
	owner.must(409, "POST", "/v1/payments", map[string]any{"staff_id": musaID, "amount_kobo": 500000, "purpose": "wages"})
}
