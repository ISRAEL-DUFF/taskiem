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

// Paying several people from WhatsApp (P4): a list pasted under "pay
// these" is read a line at a time and prepared as a batch, answered with
// how many people and how much and a link to check and pay it; the lines
// not read and the rows held out are said with why; staff pay saved
// accounts only; "pay my list" makes the collected list a batch; nothing
// is paid on words. When the batch's last row ends, its preparer is told
// once, on their chat; a single payment from the chat is told too.
func TestPayListFromWhatsApp(t *testing.T) {
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
	banks := []map[string]any{{"name": "Access Bank", "code": "044"}, {"name": "Guaranty Trust Bank", "code": "058"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner := w.tenant(t, "Ada Foods", "ada@chatlist.test")
	biz := uuid.MustParse(tenantOf(t, owner))
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	for _, s := range []map[string]any{
		{"name": "bayo", "title": "Bayo Rice Mills", "kind": "bank_account", "bank_name": "Access Bank", "account_number": "0123456789"},
		{"name": "chidi", "title": "Chidi Okeke", "kind": "bank_account", "bank_name": "Guaranty Trust Bank", "account_number": "0987654321"},
	} {
		owner.must(201, "POST", "/v1/saved", s)
	}
	on := "+2348031110001"
	w.bind(t, owner, on)
	key := addPasskey(t, owner, "Phone")
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	batchOf := func(said string) string {
		t.Helper()
		i := strings.Index(said, "/payments/batches/")
		if i < 0 {
			t.Fatalf("no batch link: %q", said)
		}
		return said[i+len("/payments/batches/") : i+len("/payments/batches/")+36]
	}

	// A pasted list: an account saved already (paid by its @name), a
	// saved @name, a line that is not a payment, one with no bank.
	said := w.say(t, on, "pay these for wages\nChidi 0987654321 GTBank 45k\n@bayo 30k\nnot a payment\nMusa 0123456789 30k")
	if !strings.Contains(said, "2 people, ₦75,000") || !strings.Contains(said, waPublicURL+"/payments/batches/") ||
		!strings.Contains(said, "Line 3 not read (not a payment)") || !strings.Contains(said, "Line 4 not read (Musa 0123456789 30k): Which bank?") {
		t.Fatalf("pasted: %q", said)
	}
	id := batchOf(said)
	if n := count(`SELECT count(*) FROM payments WHERE batch_id = $1 AND started_from = 'chat' AND status = 'prepared' AND purpose = 'wages' AND saved_id IS NOT NULL`, id); n != 2 {
		t.Fatalf("prepared rows: %d", n)
	}
	if n := count(`SELECT count(*) FROM payment_batches WHERE id = $1 AND kind = 'list' AND status = 'prepared'`, id); n != 1 {
		t.Fatalf("the batch: %d", n)
	}
	if n := count(`SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND action = 'payment.batch_chat' AND target = $2`, biz, id); n != 1 {
		t.Errorf("audited: %d", n)
	}
	// Nothing pays on words: a yes sends nothing.
	w.say(t, on, "yes")
	if n := count(`SELECT count(*) FROM payments WHERE tenant_id = $1 AND status <> 'prepared'`, biz); n != 0 {
		t.Fatalf("paid on words: %d", n)
	}
	// Nothing read: no batch.
	before := count(`SELECT count(*) FROM payment_batches WHERE tenant_id = $1`, biz)
	if said := w.say(t, on, "pay these\nhello\nthere"); !strings.Contains(said, "Nothing was prepared") || !strings.Contains(said, "Line 2 not read (there)") {
		t.Fatalf("nothing read: %q", said)
	}
	if said := w.say(t, on, "pay these"); !strings.Contains(said, "one person a line") {
		t.Fatalf("no list: %q", said)
	}
	if n := count(`SELECT count(*) FROM payment_batches WHERE tenant_id = $1`, biz); n != before {
		t.Fatalf("a batch made of nothing: %d", n-before)
	}

	// Staff pay saved accounts only: an account number not saved is held
	// out with why; a saved one goes by its name.
	owner.must(201, "PUT", "/v1/roles/payer", map[string]any{"permissions": []string{"money.pay"}})
	clerk := w.member(t, owner, "clerk@chatlist.test", "payer")
	cn := "+2348031110002"
	w.bind(t, clerk, cn)
	other := fitting(t, "044", "111222333")
	said = w.say(t, cn, "pay these\nChidi 0987654321 GTBank 10k\nDayo "+other+" Access Bank 20k")
	if !strings.Contains(said, "1 person, ₦10,000") || !strings.Contains(said, "Held out") || !strings.Contains(said, "Staff pay the business's saved accounts") {
		t.Fatalf("staff: %q", said)
	}
	if n := count(`SELECT count(*) FROM payments WHERE batch_id = $1 AND saved_id IS NOT NULL`, batchOf(said)); n != 1 {
		t.Fatalf("staff's row: %d", n)
	}
	viewer := w.member(t, owner, "viewer@chatlist.test", "viewer")
	vn := "+2348031110003"
	w.bind(t, viewer, vn)
	if said := w.say(t, vn, "pay these\n@bayo 10k"); strings.Contains(said, "people") || strings.Contains(said, "person,") {
		t.Fatalf("a viewer paid: %q", said)
	}

	// "pay my list": the collected list becomes a batch.
	owner.must(201, "POST", "/v1/pay-collection", map[string]any{"payee": "chidi", "amount_kobo": 1500000,
		"source": map[string]any{"kind": "link", "chat": "c1", "message": "m1", "from": "Funke"}})
	said = w.say(t, on, "pay my list for wages")
	if !strings.Contains(said, "1 person, ₦15,000") {
		t.Fatalf("pay my list: %q", said)
	}
	mine := batchOf(said)
	if n := count(`SELECT count(*) FROM pay_collected WHERE batch_id = $1`, mine); n != 1 {
		t.Fatalf("the list waits with the batch: %d", n)
	}
	if n := count(`SELECT count(*) FROM payments WHERE batch_id = $1 AND started_from = 'chat' AND purpose = 'wages'`, mine); n != 1 {
		t.Fatalf("the list's payment: %d", n)
	}
	if said := w.say(t, on, "pay the list"); !strings.Contains(said, "waits in a batch") {
		t.Fatalf("again: %q", said)
	}

	// Results back in the chat: the batch is confirmed in the app with a
	// passkey; one row paid, one failed: one message, when the last ends.
	b := owner.must(200, "GET", "/v1/payment-batches/"+id, nil)["batch"].(map[string]any)
	var rows []string
	for _, x := range b["payments"].([]any) {
		rows = append(rows, x.(map[string]any)["id"].(string))
	}
	owner.must(202, "POST", "/v1/payment-batches/"+id+"/confirm", map[string]any{"payments": rows, "passkey": stepUp(t, owner, key, "money.pay.batch", "batch:"+id)})
	runOf := func(payment string) uuid.UUID {
		t.Helper()
		var r uuid.UUID
		if err := w.env.DB.Admin.QueryRow(ctx, `SELECT run_id FROM payments WHERE id = $1`, payment).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	sentBefore := len(w.graph.Sent(on))
	if _, err := w.srv.PaymentsOps().Finish(ctx, biz, runOf(rows[0]), uuid.MustParse(rows[0]), "paid", "CHIDI OKEKE", "po1", "", nil); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	if got := w.graph.Sent(on)[sentBefore:]; len(got) != 0 {
		t.Fatalf("told before the last row ended: %+v", got)
	}
	for range 2 { // the same end twice tells once
		if _, err := w.srv.PaymentsOps().Finish(ctx, biz, runOf(rows[1]), uuid.MustParse(rows[1]), "failed", "", "", "The bank refused the account.", nil); err != nil {
			t.Fatal(err)
		}
	}
	w.tick(t)
	got := w.graph.Sent(on)[sentBefore:]
	if len(got) != 1 || !strings.Contains(got[0].Text, "Paid 1 of 2 people, ₦45,000.") ||
		!strings.Contains(got[0].Text, "1 not paid: BAYO RICE MILLS, The bank refused the account.") || !strings.Contains(got[0].Text, "/payments/batches/"+id) {
		t.Fatalf("the batch's result: %+v", got)
	}

	// One payment from the chat: "Paid … Received by the bank."
	if said := w.say(t, on, "send 2,500 to @bayo for a delivery"); !strings.Contains(said, "Ready to pay") {
		t.Fatalf("one: %q", said)
	}
	var one string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT id FROM payments WHERE tenant_id = $1 AND batch_id IS NULL AND started_from = 'chat' ORDER BY created_at DESC LIMIT 1`, biz).Scan(&one); err != nil {
		t.Fatal(err)
	}
	owner.must(202, "POST", "/v1/payments/"+one+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+one), "again": true})
	sentBefore = len(w.graph.Sent(on))
	if _, err := w.srv.PaymentsOps().Finish(ctx, biz, runOf(one), uuid.MustParse(one), "paid", "BAYO RICE MILLS", "po2", "", nil); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	if got := w.graph.Sent(on)[sentBefore:]; len(got) != 1 || !strings.Contains(got[0].Text, "Paid BAYO RICE MILLS ₦2,500. Received by the bank.") {
		t.Fatalf("one's result: %+v", got)
	}
	// A payment from the app is not told in the chat.
	if n := count(`SELECT count(*) FROM whatsapp_outbox WHERE tenant_id = $1 AND kind IN ('payment', 'payment_batch')`, biz); n != 2 {
		t.Fatalf("queued results: %d", n)
	}
}
