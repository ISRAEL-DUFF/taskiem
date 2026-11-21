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
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/webauthn"
)

// payRails is a payments account that pays bank accounts and checks names.
type payRails struct{}

var payRail = invoicing.Rail{Connector: "paystack", Connection: "main", Name: "Paystack", Events: map[string]bool{},
	Caps: map[string]connector.CapabilityMap{"pay_bank": {}, "resolve_account": {}}}

func (payRails) Capable(context.Context, uuid.UUID, string) ([]invoicing.Rail, error) {
	return []invoicing.Rail{payRail}, nil
}

func (payRails) ResolveChannel(_ context.Context, _ uuid.UUID, channel, _ string) (invoicing.Rail, error) {
	if channel == payRail.Channel() {
		return payRail, nil
	}
	return invoicing.Rail{}, invoicing.ErrNoRail
}

func (payRails) Call(_ context.Context, _ uuid.UUID, _ invoicing.Rail, capability string, _ map[string]any, _ effects.KeyInput) (map[string]any, error) {
	if capability == "resolve_account" {
		return map[string]any{"account_name": "BAYO RICE MILLS"}, nil
	}
	return map[string]any{}, nil
}

// Paying one person (P1; docs/spec/chat-pay-people.md): a payment is
// prepared to a saved account, with a first payment said; a person who may
// pay confirms it with a passkey, which within their limit (an owner has
// none) is its approval and starts the pay-person workflow on the payments
// account, made with the taskiem_payments policy; the workflow records how
// it ended. The same payment again asks first; staff over their limit, or
// paying an account changed since it was last paid, wait for the owner's
// OK; a workflow prepares one, which a person still confirms.
func TestPayOnePerson(t *testing.T) {
	ctx := context.Background()
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
	banks := []map[string]any{{"name": "Access Bank", "code": "044"}, {"name": "Wema Bank Plc", "code": "035"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner := w.tenant(t, "Ada Foods", "ada@pay.test")
	biz := uuid.MustParse(tenantOf(t, owner))
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	bayo := owner.must(201, "POST", "/v1/saved", map[string]any{"name": "bayo", "title": "Bayo Rice Mills", "kind": "bank_account",
		"bank_name": "Access Bank", "account_number": "0123456789"})["saved"].(map[string]any)
	key := addPasskey(t, owner, "Phone")

	// Prepared: what the person confirming should know.
	owner.must(404, "POST", "/v1/payments", map[string]any{"payee": "nobody", "amount_kobo": 4500000})
	owner.must(400, "POST", "/v1/payments", map[string]any{"payee": "bayo", "amount_kobo": 4500000, "purpose": "gifts"})
	x := owner.must(201, "POST", "/v1/payments", map[string]any{"payee": "@bayo", "amount_kobo": 4500000, "purpose": "delivery"})["payment"].(map[string]any)
	id := x["id"].(string)
	if x["status"] != "prepared" || x["account"] != "••6789" || x["bank_name"] != "Access Bank" || x["payee"] != "Bayo Rice Mills" || x["first_payment"] != true {
		t.Fatalf("prepared: %v", x)
	}
	if strings.Contains(stringOf(x), "0123456789") {
		t.Fatalf("the account number in full: %v", x)
	}

	// Confirmed with a passkey for this payment only.
	if e := owner.must(403, "POST", "/v1/payments/"+id+"/confirm", map[string]any{}); e["step_up"] == nil {
		t.Fatalf("no passkey: %v", e)
	}
	got := owner.must(202, "POST", "/v1/payments/"+id+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+id)})["payment"].(map[string]any)
	if got["status"] != "sending" || got["run_id"] == nil || got["confirmed_by"] == nil || got["needs_ok"] != "" {
		t.Fatalf("confirmed: %v", got)
	}
	run := got["run_id"].(string)
	var body string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload::text FROM run_events WHERE run_id = $1 AND type = 'RunStarted'`, run).Scan(&body); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"bank_code": "044"`, `"$pii": "account_number"`, `"amount_kobo": 4500000`, `"narration": "Taskiem: a delivery"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the run's input has no %s: %s", want, body)
		}
	}
	// The payer's passkey approved it: the run went on to check the name.
	var decided int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE run_id = $1 AND type = 'ApprovalDecided'`, run).Scan(&decided); err != nil || decided != 1 {
		t.Errorf("approved by the payer: %d %v", decided, err)
	}
	owner.must(409, "POST", "/v1/payments/"+id+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+id)})
	// The workflow records how it ended.
	if _, err := w.srv.PaymentsOps().Finish(ctx, biz, uuid.MustParse(id), "paid", "BAYO RICE MILLS", "trf_1", "", nil); err != nil {
		t.Fatal(err)
	}
	if p := owner.must(200, "GET", "/v1/payments/"+id, nil)["payment"].(map[string]any); p["status"] != "paid" || p["account_name"] != "BAYO RICE MILLS" {
		t.Fatalf("paid: %v", p)
	}

	// Two confirms at once: one sends it, the other is told it was sent.
	twice := owner.must(201, "POST", "/v1/payments", map[string]any{"payee": "bayo", "amount_kobo": 120000, "purpose": "bill"})["payment"].(map[string]any)["id"].(string)
	proofs := []any{stepUp(t, owner, key, "money.pay", "payment:"+twice), stepUp(t, owner, key, "money.pay", "payment:"+twice)}
	codes := make(chan int, 2)
	for _, pf := range proofs {
		go func() {
			code, _ := owner.do("POST", "/v1/payments/"+twice+"/confirm", map[string]any{"passkey": pf})
			codes <- code
		}()
	}
	if a, b := <-codes, <-codes; a+b != 202+409 {
		t.Fatalf("two confirms at once: %d %d", a, b)
	}
	if _, err := w.srv.PaymentsOps().Finish(ctx, biz, uuid.MustParse(twice), "paid", "BAYO RICE MILLS", "trf_2", "", nil); err != nil {
		t.Fatal(err)
	}

	// The same again in a day: asked first.
	again := owner.must(201, "POST", "/v1/payments", map[string]any{"payee": "bayo", "amount_kobo": 4500000, "purpose": "delivery"})["payment"].(map[string]any)
	aid := again["id"].(string)
	if again["again"] != true {
		t.Fatalf("again not said: %v", again)
	}
	if e := owner.must(409, "POST", "/v1/payments/"+aid+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+aid)}); e["code"] != "pay_again" {
		t.Fatalf("again: %v", e)
	}
	owner.must(200, "POST", "/v1/payments/"+aid+"/cancel", nil)

	// Staff who may pay: nothing without the owner's OK until a limit is set.
	owner.must(201, "PUT", "/v1/roles/payer", map[string]any{"permissions": []string{"money.pay"}})
	clerk := w.member(t, owner, "clerk@pay.test", "payer")
	ckey := addPasskey(t, clerk, "Phone")
	prep := func(amount int) string {
		t.Helper()
		return clerk.must(201, "POST", "/v1/payments", map[string]any{"payee": "bayo", "amount_kobo": amount, "purpose": "stock"})["payment"].(map[string]any)["id"].(string)
	}
	cid := prep(1000000)
	if got := clerk.must(202, "POST", "/v1/payments/"+cid+"/confirm", map[string]any{"passkey": stepUp(t, clerk, ckey, "money.pay", "payment:"+cid)})["payment"].(map[string]any); got["status"] != "waiting_ok" || got["needs_ok"] != "limit" {
		t.Fatalf("over a limit of nothing: %v", got)
	}
	clerk.must(403, "PUT", "/v1/pay-limits/payer", map[string]any{"one_kobo": 5000000, "day_kobo": 10000000})
	owner.must(200, "PUT", "/v1/pay-limits/payer", map[string]any{"one_kobo": 5000000, "day_kobo": 10000000})
	cid = prep(2000000)
	if got := clerk.must(202, "POST", "/v1/payments/"+cid+"/confirm", map[string]any{"passkey": stepUp(t, clerk, ckey, "money.pay", "payment:"+cid)})["payment"].(map[string]any); got["status"] != "sending" {
		t.Fatalf("within the limit: %v", got)
	}

	// The account changed since it was last paid: the owner's OK, whoever
	// pays within their limit.
	ch := challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.change", "target": bayo["id"]}))
	owner.must(200, "PUT", "/v1/saved/"+bayo["id"].(string), map[string]any{"account_number": "0987654321", "passkey": key.Assert(ch).JSON()})
	cid = prep(100000)
	if got := clerk.must(202, "POST", "/v1/payments/"+cid+"/confirm", map[string]any{"passkey": stepUp(t, clerk, ckey, "money.pay", "payment:"+cid)})["payment"].(map[string]any); got["status"] != "waiting_ok" || got["needs_ok"] != "changed" || got["changed_account"] != true {
		t.Fatalf("a changed account: %v", got)
	}

	// Who may not pay, does not.
	viewer := w.member(t, owner, "vic@pay.test", "viewer")
	viewer.must(403, "POST", "/v1/payments", map[string]any{"payee": "bayo", "amount_kobo": 100000})
	viewer.must(403, "GET", "/v1/payments", nil)

	// An account number not saved: its name checked as it is prepared.
	acct := owner.must(201, "POST", "/v1/payments", map[string]any{"account_number": "0123456789", "bank_name": "Access", "amount_kobo": 300000, "purpose": "stock"})["payment"].(map[string]any)
	if acct["account_name"] != "BAYO RICE MILLS" || acct["payee"] != "BAYO RICE MILLS" || acct["saved_id"] != nil {
		t.Fatalf("an account number: %v", acct)
	}
	owner.must(400, "POST", "/v1/payments", map[string]any{"account_number": "01234", "bank_name": "Access Bank", "amount_kobo": 300000})
	owner.must(200, "POST", "/v1/payments/"+acct["id"].(string)+"/cancel", nil)

	// The Pay topic, for those who may pay; its forms confirm with a passkey.
	var pay map[string]any
	for _, tp := range owner.must(200, "GET", "/v1/chat/actions", nil)["topics"].([]any) {
		if tp.(map[string]any)["id"] == "pay" {
			pay = tp.(map[string]any)
		}
	}
	if pay == nil || pay["actions"].([]any)[0].(map[string]any)["confirm"] != "passkey" {
		t.Fatalf("the pay topic: %v", pay)
	}
	if strings.Contains(stringOf(viewer.must(200, "GET", "/v1/chat/actions", nil)), `"pay.person"`) {
		t.Errorf("a viewer offered paying")
	}
	if ch := owner.must(200, "GET", "/v1/chat/actions/pay.person/choices", nil); !strings.Contains(stringOf(ch), "@bayo") {
		t.Errorf("payee choices: %v", ch)
	}

	// From the chat: prepared, never sent, with where to confirm it.
	w.srv.PublicURL = "https://app.taskiem.test"
	if said := taskiemSays(t, owner, "send 2,500 to @bayo for a delivery: rice"); !strings.Contains(said, "Ready to pay ₦2,500 to Bayo Rice Mills") ||
		!strings.Contains(said, "for a delivery") || !strings.Contains(said, "https://app.taskiem.test/payments/") {
		t.Fatalf("from the chat: %q", said)
	}
	if said := taskiemSays(t, owner, "transfer 3k to 0123456789 for stock"); !strings.Contains(said, "Which bank") {
		t.Fatalf("no bank: %q", said)
	}
	if said := taskiemSays(t, owner, "transfer 3k to 0123456789 access for stock bought"); !strings.Contains(said, "BAYO RICE MILLS") {
		t.Fatalf("an account number from the chat: %q", said)
	}
	if said := taskiemSays(t, owner, "pay @nobody 3k"); !strings.Contains(said, "Not prepared") {
		t.Fatalf("to nobody: %q", said)
	}
	if said := taskiemSays(t, viewer, "send 2,500 to @bayo for a delivery"); strings.Contains(said, "Ready to pay") {
		t.Fatalf("a viewer paid from the chat: %q", said)
	}
	var fromChat int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM payments WHERE tenant_id = $1 AND started_from = 'chat' AND status = 'prepared'`, biz).Scan(&fromChat); err != nil || fromChat != 2 {
		t.Errorf("prepared from the chat: %d %v", fromChat, err)
	}

	// A workflow prepares one; a person confirms it.
	out, err := w.srv.PaymentsOps().Prepare(ctx, biz, uuid.Must(uuid.NewV7()), payconn.PrepareIn{Payee: "bayo", AmountKobo: 250000, Purpose: "bill"})
	if err != nil || out["status"] != "prepared" {
		t.Fatalf("a workflow's: %v %v", out, err)
	}
	if _, err := w.srv.PaymentsOps().Prepare(ctx, biz, uuid.Must(uuid.NewV7()), payconn.PrepareIn{Payee: "nobody", AmountKobo: 250000, Purpose: "bill"}); err == nil {
		t.Errorf("a workflow's to nobody")
	}
	list := owner.must(200, "GET", "/v1/payments", nil)["payments"].([]any)
	if len(list) < 8 || list[0].(map[string]any)["started_from"] != "workflow" {
		t.Errorf("list: %v", list)
	}
}
