package api_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	payconn "github.com/israel-duff/taskiem/connectors/payments"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// payPinWorld is a business that pays bayo, its owner with a passkey and
// the PIN 482913, set two days ago.
func payPinWorld(t *testing.T) (*waWorld, *client) {
	t.Helper()
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
	banks := []map[string]any{{"name": "Access Bank", "code": "044"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner := w.tenant(t, "Ada Foods", "ada@pinpay.test")
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	owner.must(201, "POST", "/v1/saved", map[string]any{"name": "bayo", "title": "Bayo Rice Mills", "kind": "bank_account",
		"bank_name": "Access Bank", "account_number": "0123456789"})
	if _, err := w.env.DB.Admin.Exec(ctx, `SELECT taskiem_wa_pin_set($1, $2)`, userID(t, owner), pinHash("482913")); err != nil {
		t.Fatal(err)
	}
	pinAge(t, w, owner, "2 days")
	return w, owner
}

// pinAge sets when the owner's PIN was set, so long ago.
func pinAge(t *testing.T, w *waWorld, c *client, ago string) {
	t.Helper()
	if _, err := w.env.DB.Admin.Exec(context.Background(), `UPDATE whatsapp_pins SET set_at = now() - $2::interval, pays_from = now() - $2::interval + interval '24 hours', locked_until = NULL, failures = 0, failed_since = NULL
		WHERE user_id = $1`, userID(t, c), ago); err != nil {
		t.Fatal(err)
	}
}

// The PIN as the passkey's fallback, in the app's own pad: within its
// limits (₦100,000 a payment, ₦200,000 a day) it pays, with step-up
// "pin"; over them, to a changed account, with a PIN set in the last day
// or after five wrong tries, it does not; an authenticator code never
// pays.
func TestPayByPin(t *testing.T) {
	ctx := context.Background()
	w, owner := payPinWorld(t)
	key := addPasskey(t, owner, "Phone")
	prep := func(naira int) string {
		t.Helper()
		return owner.must(201, "POST", "/v1/payments", map[string]any{"payee": "bayo", "amount_kobo": naira * 100, "purpose": "delivery"})["payment"].(map[string]any)["id"].(string)
	}
	confirm := func(id string, body map[string]any) (int, map[string]any) {
		t.Helper()
		return owner.do("POST", "/v1/payments/"+id+"/confirm", body)
	}

	// Asked with no proof: the passkey, or the PIN within its limit.
	id := prep(45000)
	if st, e := confirm(id, map[string]any{}); st != 403 || stringOf(e["methods"]) != stringOf([]any{"passkey", "pin"}) || e["pin_limit_kobo"] != float64(10000000) {
		t.Fatalf("step-up offered: %d %v", st, e)
	}
	// An authenticator code still does not pay.
	if _, e := confirm(id, map[string]any{"totp": "123456"}); e["code"] != "passkey" {
		t.Fatalf("a code: %v", e)
	}
	// The right PIN pays.
	got := owner.must(202, "POST", "/v1/payments/"+id+"/confirm", map[string]any{"pin": "482913"})["payment"].(map[string]any)
	if got["status"] != "sending" || got["run_id"] == nil {
		t.Fatalf("paid by PIN: %v", got)
	}
	var with, su string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT p.confirmed_with, d.step_up FROM payments p JOIN approval_decisions d ON d.run_id = p.run_id WHERE p.id = $1`, id).
		Scan(&with, &su); err != nil || with != "pin" || su != "pin" {
		t.Fatalf("confirmed with: %q %q %v", with, su, err)
	}

	// Over ₦100,000: the passkey only.
	big := prep(150000)
	if _, e := confirm(big, map[string]any{}); stringOf(e["methods"]) != stringOf([]any{"passkey"}) {
		t.Errorf("the PIN offered over its limit: %v", e)
	}
	if st, e := confirm(big, map[string]any{"pin": "482913"}); st != 403 || e["code"] != "pin_limit" || !strings.Contains(fmt.Sprint(e["error"]), "use your passkey") {
		t.Fatalf("over ₦100,000: %d %v", st, e)
	}
	// The day's ₦200,000: ₦45,000 + ₦90,000 fit, ₦80,000 more does not.
	owner.must(202, "POST", "/v1/payments/"+prep(90000)+"/confirm", map[string]any{"pin": "482913"})
	over := prep(80000)
	if _, e := confirm(over, map[string]any{}); stringOf(e["methods"]) != stringOf([]any{"passkey"}) {
		t.Errorf("the PIN offered over its day: %v", e)
	}
	if st, e := confirm(over, map[string]any{"pin": "482913"}); st != 403 || e["code"] != "pin_limit" || !strings.Contains(fmt.Sprint(e["error"]), "a day") {
		t.Fatalf("over the day: %d %v", st, e)
	}
	// The passkey still pays it.
	owner.must(202, "POST", "/v1/payments/"+over+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+over)})

	// A PIN set an hour ago cannot pay yet.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE payments SET confirmed_at = now() - interval '2 days' WHERE confirmed_with = 'pin'`); err != nil {
		t.Fatal(err)
	}
	pinAge(t, w, owner, "1 hour")
	small := prep(10000)
	if st, e := confirm(small, map[string]any{"pin": "482913"}); st != 403 || e["code"] != "pin_new" {
		t.Fatalf("a new PIN: %d %v", st, e)
	}
	if _, e := confirm(small, map[string]any{}); stringOf(e["methods"]) != stringOf([]any{"passkey"}) {
		t.Errorf("a new PIN offered: %v", e)
	}
	pinAge(t, w, owner, "2 days")

	// Wrong PINs are counted with the approvals': five lock it. A wrong
	// PIN changes nothing of the payment, the sheet's ticks neither (H7).
	for i := range 4 {
		if st, e := confirm(small, map[string]any{"pin": fmt.Sprintf("00000%d", i), "tell_payee": false, "send_receipt": true}); st != 403 || e["code"] != "pin_wrong" ||
			!strings.Contains(fmt.Sprint(e["error"]), fmt.Sprintf("%d tries left", 4-i)) {
			t.Fatalf("wrong %d: %d %v", i, st, e)
		}
	}
	var tell, receipt bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT tell_payee, send_receipt FROM payments WHERE id = $1`, small).Scan(&tell, &receipt); err != nil || !tell || receipt {
		t.Fatalf("a wrong PIN changed the ticks: %v %v %v", tell, receipt, err)
	}
	if _, e := confirm(small, map[string]any{"pin": "000009"}); e["code"] != "pin_locked" {
		t.Fatalf("the fifth: %v", e)
	}
	if _, e := confirm(small, map[string]any{"pin": "482913"}); e["code"] != "pin_locked" {
		t.Fatalf("locked, the right PIN: %v", e)
	}
	var failures int
	var locked bool
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT failures, locked_until > now() FROM whatsapp_pins WHERE user_id = $1`, userID(t, owner)).Scan(&failures, &locked); err != nil || !locked {
		t.Errorf("the shared lock: %d %v %v", failures, locked, err)
	}
	audit := toJSON(owner.must(200, "GET", "/v1/audit", nil))
	for _, a := range []string{`"payment.pin.fail"`, `"payment.pin.locked"`, `"step_up":"pin"`} {
		if !strings.Contains(audit, a) {
			t.Errorf("audit lacks %s", a)
		}
	}
	pinAge(t, w, owner, "2 days")
	if p := owner.must(200, "GET", "/v1/payments/"+small, nil)["payment"].(map[string]any); p["status"] != "prepared" {
		t.Errorf("refused, but not prepared: %v", p)
	}

	// A changed account: the passkey only.
	var bayo string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT saved_id FROM payments WHERE id = $1`, id).Scan(&bayo); err != nil {
		t.Fatal(err)
	}
	owner.must(200, "PUT", "/v1/saved/"+bayo, map[string]any{"account_number": "0987654321",
		"passkey": key.Assert(challengeFrom(t, owner.must(200, "POST", "/v1/saved/step-up/options", map[string]any{"operation": "saved.change", "target": bayo}))).JSON()})
	moved := prep(20000)
	if st, e := confirm(moved, map[string]any{"pin": "482913"}); st != 403 || e["code"] != "pin_passkey_only" {
		t.Fatalf("a changed account: %d %v", st, e)
	}
	// A row of a list: the passkey only, as the list is.
	b := owner.must(201, "POST", "/v1/payment-batches", map[string]any{"rows": []map[string]any{{"payee": "bayo", "amount_kobo": 2000000, "purpose": "delivery"}}})["batch"].(map[string]any)
	row := b["payments"].([]any)[0].(map[string]any)["id"].(string)
	if st, e := confirm(row, map[string]any{"pin": "482913"}); st != 403 || e["code"] != "pin_passkey_only" {
		t.Fatalf("a row of a list: %d %v", st, e)
	}

	// No PIN: said.
	if _, err := w.env.DB.Admin.Exec(ctx, `SELECT taskiem_wa_pin_remove($1)`, userID(t, owner)); err != nil {
		t.Fatal(err)
	}
	if _, e := confirm(small, map[string]any{"pin": "482913"}); e["code"] != "no_pin" {
		t.Fatalf("no PIN: %v", e)
	}
}

// The PIN in WhatsApp's form, bound to one payment prepared there: a
// wrong PIN keeps the form open, the right one pays and the chat says so;
// over the PIN's limit no form is sent, only the passkey's link.
func TestPayByPinWhatsAppFlow(t *testing.T) {
	ctx := context.Background()
	w, owner := payPinWorld(t)
	w.flows()
	w.srv.PublicURL = "https://app.taskiem.test"
	const an = "+2348010000071"
	w.bind(t, owner, an)
	w.say(t, an, "hi")

	got := w.send(t, an, whatsapptest.Text("send 45k to @bayo for a delivery"))
	if len(got) != 2 || !strings.Contains(got[0].Text, "https://app.taskiem.test/payments/") {
		t.Fatalf("prepared: %+v", got)
	}
	f := got[1]
	if f.Flow != whatsapp.FlowPin || f.FlowScreen != "PIN" || f.FlowData["title"] != "Pay ₦45,000" || !strings.Contains(f.Text, "PIN") {
		t.Fatalf("the PIN form: %+v", f)
	}
	var kind, payment string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT kind, data->>'payment' FROM whatsapp_flows WHERE kind = 'pay_pin' AND expires_at < now() + interval '11 minutes'`).
		Scan(&kind, &payment); err != nil {
		t.Fatal(err)
	}
	const ep = "/channels/whatsapp/flows"
	st, resp := w.flowCall(t, ep, waSecret, "data_exchange", "PIN", f.FlowToken, map[string]any{"pin": "000000"})
	if st != 200 || !strings.Contains(fmt.Sprint(screenData(t, resp)["error"]), "4 tries left") {
		t.Fatalf("wrong PIN: %d %v", st, resp)
	}
	st, resp = w.flowCall(t, ep, waSecret, "data_exchange", "PIN", f.FlowToken, map[string]any{"pin": "482913"})
	if st != 200 || flowToken(t, resp) != f.FlowToken {
		t.Fatalf("right PIN: %d %v", st, resp)
	}
	if st, _ := w.flowCall(t, ep, waSecret, "data_exchange", "PIN", f.FlowToken, map[string]any{"pin": "482913"}); st != 427 {
		t.Errorf("replayed: %d", st)
	}
	if done := w.send(t, an, whatsapptest.FlowCompleted(f.FlowToken)); len(done) != 1 || !strings.Contains(done[0].Text, "Sent: BAYO RICE MILLS ₦45,000") {
		t.Fatalf("completion: %+v", done)
	}
	var status, with, channel string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT p.status, p.confirmed_with, d.channel FROM payments p JOIN approval_decisions d ON d.run_id = p.run_id WHERE p.id = $1`, payment).
		Scan(&status, &with, &channel); err != nil || status != "sending" || with != "pin" || channel != "whatsapp" {
		t.Fatalf("paid: %q %q %q %v", status, with, channel, err)
	}

	// Over ₦100,000: the link only.
	if got := w.send(t, an, whatsapptest.Text("send 150k to @bayo for stock")); len(got) != 1 || got[0].Flow != "" {
		t.Errorf("a form over the PIN's limit: %+v", got)
	}
	// Refused after the form was sent: closed with why.
	got = w.send(t, an, whatsapptest.Text("send 30k to @bayo for stock"))
	if len(got) != 2 {
		t.Fatalf("a second form: %+v", got)
	}
	pinAge(t, w, owner, "1 hour")
	st, resp = w.flowCall(t, ep, waSecret, "data_exchange", "PIN", got[1].FlowToken, map[string]any{"pin": "482913"})
	if st != 200 || !strings.Contains(fmt.Sprint(screenData(t, resp)["error"]), "last 24 hours") {
		t.Fatalf("a new PIN: %d %v", st, resp)
	}
	if done := w.send(t, an, whatsapptest.FlowCompleted(got[1].FlowToken)); len(done) != 1 || !strings.Contains(done[0].Text, "Not sent") {
		t.Errorf("refused completion: %+v", done)
	}
}

// The PIN set at sign-up and reset without texting it (pin_me.go): a
// first PIN needs no passkey but pays only a day on; changing it later
// takes a passkey (and then pays at once) or a sign-in moments ago (and
// pays a day on); nothing changes it otherwise.
func TestMePin(t *testing.T) {
	ctx := context.Background()
	w := newWAWorld(t)
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.LoginBurst = 100
	ada := w.tenant(t, "Ada Foods", "ada@mepin.test")
	me := userID(t, ada)
	paysFrom := func(got map[string]any) time.Duration {
		t.Helper()
		at, err := time.Parse(time.RFC3339Nano, got["pays_from"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return time.Until(at)
	}

	// None yet: set without a passkey, as at sign-up; it pays a day on.
	if got := ada.must(200, "GET", "/v1/me/pin", nil); got["set"] != false || got["change_without"] != true {
		t.Fatalf("before: %v", got)
	}
	ada.must(400, "PUT", "/v1/me/pin", map[string]any{"pin": "111111"})
	got := ada.must(200, "PUT", "/v1/me/pin", map[string]any{"pin": "482913"})
	if d := paysFrom(got); got["set"] != true || d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("a first PIN: %v", got)
	}

	// Signed in moments ago: changed without a passkey, a day on again.
	if got := ada.must(200, "PUT", "/v1/me/pin", map[string]any{"pin": "590314"}); paysFrom(got) < 23*time.Hour {
		t.Fatalf("reset after signing in: %v", got)
	}

	// Signed in long ago: not without a passkey.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE sessions SET created_at = now() - interval '1 hour' WHERE user_id = $1`, me); err != nil {
		t.Fatal(err)
	}
	if got := ada.must(200, "GET", "/v1/me/pin", nil); got["change_without"] != false {
		t.Fatalf("an old sign-in: %v", got)
	}
	if e := ada.must(403, "PUT", "/v1/me/pin", map[string]any{"pin": "720581"}); e["code"] != "proof_needed" {
		t.Fatalf("no proof: %v", e)
	}
	// A code for an authenticator they do not have proves nothing.
	if e := ada.must(403, "PUT", "/v1/me/pin", map[string]any{"pin": "720581", "totp": "123456"}); e["code"] != "proof_needed" {
		t.Fatalf("a junk code: %v", e)
	}
	// Behind a passkey: it pays at once.
	key := addPasskey(t, ada, "Phone")
	got = ada.must(200, "PUT", "/v1/me/pin", map[string]any{"pin": "720581", "passkey": stepUp(t, ada, key, "account.reauth", "pin.set")})
	if d := paysFrom(got); d > time.Minute {
		t.Fatalf("behind a passkey: %v", got)
	}
	var n int
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'pin.set'`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("audited: %d %v", n, err)
	}
	// Removed with no proof; then none is set.
	ada.must(204, "DELETE", "/v1/me/pin", nil)
	if got := ada.must(200, "GET", "/v1/me/pin", nil); got["set"] != false {
		t.Fatalf("removed: %v", got)
	}
	ada.must(404, "DELETE", "/v1/me/pin", nil)

	// Removed is not "none set": setting one again takes what a change
	// takes (an old session cannot), and is never a first PIN.
	if got := ada.must(200, "GET", "/v1/me/pin", nil); got["change_without"] != false {
		t.Fatalf("after removing, an old sign-in: %v", got)
	}
	if e := ada.must(403, "PUT", "/v1/me/pin", map[string]any{"pin": "720581"}); e["code"] != "proof_needed" {
		t.Fatalf("after removing, no proof: %v", e)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE sessions SET created_at = now() WHERE user_id = $1`, me); err != nil {
		t.Fatal(err)
	}
	if got := ada.must(200, "PUT", "/v1/me/pin", map[string]any{"pin": "720581"}); paysFrom(got) < 23*time.Hour {
		t.Fatalf("after removing, signed in again: %v", got)
	}
	var via string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT detail->>'via' FROM audit_log WHERE action = 'pin.set' ORDER BY id DESC LIMIT 1`).Scan(&via); err != nil || via != "signed_in_again" {
		t.Fatalf("after removing, via %q %v", via, err)
	}
}
