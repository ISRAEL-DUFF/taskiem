package api_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	payconn "github.com/israel-duff/taskiem/connectors/payments"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/riders"
	"github.com/israel-duff/taskiem/engine/saved"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

// fitting is a 10-digit account number whose check digit fits code.
func fitting(t *testing.T, code, nine string) string {
	t.Helper()
	for d := '0'; d <= '9'; d++ {
		if fits, _ := saved.NUBANFits(code, nine+string(d)); fits {
			return nine + string(d)
		}
	}
	t.Fatalf("no check digit for %s %s", code, nine)
	return ""
}

// Paying from a message (paying people, phase 2): account details in a
// message are read for a payment (number, bank in the payments account's
// list, amount, name); with no bank, the banks the number fits come first,
// and the name is checked at the likely ones at once; a number paid
// before says so; a rider is paid to the account kept for their payouts.
func TestPayFromAMessage(t *testing.T) {
	ctx := context.Background()
	w := newReachWorld(t)
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
	banks := []map[string]any{{"name": "Access Bank", "code": "044"}, {"name": "Wema Bank Plc", "code": "035"}, {"name": "Guaranty Trust Bank", "code": "058"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner := w.tenant(t, "Ada Foods", "ada@msg.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})

	gtb := fitting(t, "058", "012345678")
	// A message with all of it.
	got := owner.must(200, "POST", "/v1/payments/read", map[string]any{"text": "Pls send 45k to " + gtb + " GTBank\nAccount name: Chidi Okeke"})
	if got["found"] != true || got["account_number"] != gtb || got["bank_name"] != "Guaranty Trust Bank" || got["bank_code"] != "058" ||
		got["amount_kobo"] != float64(4500000) || got["name"] != "Chidi Okeke" || got["paid"] != nil {
		t.Fatalf("read: %v", got)
	}
	if got := owner.must(200, "POST", "/v1/payments/read", map[string]any{"text": "See you at 5, order TK-12345"}); got["found"] != false {
		t.Fatalf("no account: %v", got)
	}
	// No bank: the banks the number fits come first.
	only := owner.must(200, "POST", "/v1/payments/read", map[string]any{"text": "acct no " + gtb})
	likely := only["likely"].([]any)
	if only["bank_code"] != nil || len(likely) == 0 || likely[0].(map[string]any)["code"] != "058" {
		t.Fatalf("likely banks: %v", only)
	}
	list := owner.must(200, "GET", "/v1/payments/banks?account="+gtb, nil)["banks"].([]any)
	if first := list[0].(map[string]any); first["code"] != "058" || first["likely"] != true {
		t.Fatalf("banks for the number: %v", list)
	}
	// The name checked at the likely ones at once.
	checked := owner.must(200, "POST", "/v1/payments/name-check", map[string]any{"account_number": gtb})["banks"].([]any)
	if len(checked) == 0 || len(checked) > 3 || checked[0].(map[string]any)["account_name"] != "BAYO RICE MILLS" {
		t.Fatalf("likely checked: %v", checked)
	}
	owner.must(400, "POST", "/v1/payments/name-check", map[string]any{"account_number": "123"})

	// Saved already: the reader says by which name.
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "chidi", "title": "Chidi Okeke", "kind": "bank_account", "bank_name": "Guaranty Trust Bank", "account_number": gtb})
	if got := owner.must(200, "POST", "/v1/payments/read", map[string]any{"text": gtb + " gtb"}); got["payee"] != "chidi" {
		t.Fatalf("saved already: %v", got)
	}

	// Paid before: the chip says so.
	key := addPasskey(t, owner, "Phone")
	x := owner.must(201, "POST", "/v1/payments", map[string]any{"account_number": gtb, "bank_name": "GTBank", "amount_kobo": 4500000, "purpose": "delivery"})["payment"].(map[string]any)
	owner.must(202, "POST", "/v1/payments/"+x["id"].(string)+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+x["id"].(string))})
	if got := owner.must(200, "POST", "/v1/payments/read", map[string]any{"text": gtb + " gtb"}); got["paid"] == nil || got["paid"].(map[string]any)["amount_kobo"] != float64(4500000) {
		t.Fatalf("paid before: %v", got)
	}

	// A rider, paid to the account kept for their payouts.
	w.srv.Riders = &riders.Riders{Pool: w.env.DB.App, Vault: w.env.Vault, Book: w.srv.Reach, Messages: w.srv.Messages, PageBase: "https://forms.taskiem.test"}
	w.srv.Dispatch = &dispatch.Dispatch{Pool: w.env.DB.App, Vault: w.env.Vault, Riders: w.srv.Riders}
	biz := uuid.MustParse(tenantOf(t, owner))
	var rider uuid.UUID
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
		name, phone, fee := "Musa Bello", "+2348031110091", int64(100000)
		rd, err := w.srv.Riders.AddTx(ctx, tx, biz, riders.In{Name: &name, Phone: &phone, FeeKobo: &fee}, "user:ada")
		rider = rd.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if e := owner.must(409, "POST", "/v1/payments", map[string]any{"rider_id": rider, "amount_kobo": 300000, "purpose": "delivery"}); e["code"] != "no_bank" {
		t.Fatalf("no bank kept: %v", e)
	}
	musaAcct := fitting(t, "044", "076543210")
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
		_, err := w.srv.Dispatch.SetBankTx(ctx, tx, biz, rider, dispatch.Bank{BankCode: "044", AccountNumber: musaAcct}, "user:ada", "passkey")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p := owner.must(201, "POST", "/v1/payments", map[string]any{"rider_id": rider, "amount_kobo": 300000, "purpose": "delivery"})["payment"].(map[string]any)
	if p["payee"] != "Musa Bello" || p["account"] != "••"+musaAcct[6:] || p["saved_id"] != nil || strings.Contains(stringOf(p), musaAcct) {
		t.Fatalf("a rider: %v", p)
	}
	owner.must(404, "POST", "/v1/payments", map[string]any{"rider_id": uuid.New(), "amount_kobo": 300000})

	// The rider's account changed since they were last paid: said, and
	// needs the owner's OK from staff (the first payment there too).
	owner.must(202, "POST", "/v1/payments/"+p["id"].(string)+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+p["id"].(string))})
	other := fitting(t, "035", "076543211")
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
		_, err := w.srv.Dispatch.SetBankTx(ctx, tx, biz, rider, dispatch.Bank{BankCode: "035", AccountNumber: other}, "user:ada", "passkey")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if q := owner.must(201, "POST", "/v1/payments", map[string]any{"rider_id": rider, "amount_kobo": 300000, "purpose": "delivery"})["payment"].(map[string]any); q["changed_account"] != true {
		t.Fatalf("a rider's changed account: %v", q)
	}
	// Only owners check names at several banks.
	owner.must(200, "POST", "/v1/payments/name-check", map[string]any{"account_number": gtb})
}
