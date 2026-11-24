package api_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	payconn "github.com/israel-duff/taskiem/connectors/payments"
	"github.com/israel-duff/taskiem/connectors/paystack"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/db"
	"github.com/israel-duff/taskiem/engine/dispatch"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/invoicing"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/secrets"
	"github.com/israel-duff/taskiem/engine/sender"
	"github.com/israel-duff/taskiem/engine/webauthn"
	"github.com/israel-duff/taskiem/engine/whatsapp"
	"github.com/israel-duff/taskiem/engine/whatsapp/whatsapptest"
)

// standInRails is the stand-in payments account of paying people H7: it
// pays bank accounts and checks names, as payRails does, and also says
// its balance, quotes a payout's fee (₦25), and, as the Taskiem wallet,
// pays another Taskiem wallet. Never a real provider.
type standInRails struct {
	mu      sync.Mutex
	balance int64
	quotes  bool
	wallet  bool
	names   map[string]string
}

func (s *standInRails) rail() invoicing.Rail {
	s.mu.Lock()
	defer s.mu.Unlock()
	caps := map[string]connector.CapabilityMap{"pay_bank": {}, "resolve_account": {}, "balance": {}}
	if s.quotes {
		caps["quote_fee"] = connector.CapabilityMap{}
	}
	if s.wallet {
		caps["pay_wallet"] = connector.CapabilityMap{Requires: []string{"source"}}
		caps["balance"] = connector.CapabilityMap{Requires: []string{"account"}}
		return invoicing.Rail{Connector: "iswallet", Connection: secrets.PlatformName, Name: "iswallet", Events: map[string]bool{}, Caps: caps}
	}
	return invoicing.Rail{Connector: "paystack", Connection: "main", Name: "Paystack", Events: map[string]bool{}, Caps: caps}
}

func (s *standInRails) Capable(context.Context, uuid.UUID, string) ([]invoicing.Rail, error) {
	return []invoicing.Rail{s.rail()}, nil
}

func (s *standInRails) ResolveChannel(_ context.Context, _ uuid.UUID, channel, _ string) (invoicing.Rail, error) {
	if rl := s.rail(); channel == rl.Channel() {
		return rl, nil
	}
	return invoicing.Rail{}, invoicing.ErrNoRail
}

func (s *standInRails) Call(_ context.Context, _ uuid.UUID, _ invoicing.Rail, capability string, in map[string]any, _ effects.KeyInput) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch capability {
	case "resolve_account":
		if n, ok := s.names[fmt.Sprint(in["account_number"])]; ok {
			return map[string]any{"account_name": n}, nil
		}
		return map[string]any{"account_name": "BAYO RICE MILLS"}, nil
	case "balance":
		return map[string]any{"available_kobo": float64(s.balance), "currency": "NGN"}, nil
	case "quote_fee":
		return map[string]any{"fee_kobo": float64(2500)}, nil
	}
	return map[string]any{}, nil
}

// Paying one person, finished (build-plan-gaps H7; chat-pay-people's
// "Done when"): an owner pays a rider ₦45,000, seeing the fee and the
// balance after before confirming; a balance that does not cover the
// amount and the fee stops it; it is recorded as a delivery in the money
// log with its fee as its own line, bank charges; the chat's diary line
// names the payment; the rider is told on their chat app with the amount,
// what for, the day and their last four digits (never the fee, the
// balance or who approved), and again when the bank sends it back; the
// receipt is a PDF at a signed link; Taskiem wallet to Taskiem wallet is
// chosen where it can be; and a forwarded message with account details,
// or a reply of "pay" to one, is answered with the name and "how much?".
func TestPayOnePersonFinished(t *testing.T) {
	ctx := context.Background()
	w, owner := newDispatchWorld(t, "Ada Foods")
	w.srv.WebAuthn = webauthn.Config{RPID: rpID, RPName: "Taskiem", Origins: []string{origin}}
	w.srv.LoginBurst = 100
	mlog := &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault}
	w.srv.Invoices = &invoicing.Invoices{Pool: w.env.DB.App, Vault: w.env.Vault, PageBase: "https://forms.taskiem.test", MoneyLog: mlog,
		Messages: &sender.Queue{Vault: w.env.Vault, HashKey: bytes.Repeat([]byte{8}, 32)}, Signal: w.env.Store.DeliverSignalOnce}
	rails := &standInRails{balance: 10_000_000, quotes: true, names: map[string]string{}}
	w.srv.AccountRails = rails
	for _, c := range []*connector.Connector{payconn.New(w.srv.PaymentsOps()), paystack.New(paystack.Options{})} {
		if err := w.env.Registry.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	banks := []map[string]any{{"name": "Access Bank", "code": "044"}, {"name": "Guaranty Trust Bank", "code": "058"}}
	w.srv.BillBanks = func(context.Context, uuid.UUID, string) ([]map[string]any, error) { return banks, nil }
	owner.must(201, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "paystack@1", "name": "main", "mode": "test",
		"credentials": map[string]string{"secret_key": "sk_test_good"}})
	biz := w.biz
	// The owner on WhatsApp (bound before a passkey asks to confirm it).
	const ada = "+2348031110072"
	w.bindWA(t, owner, ada)
	const adaTG = int64(7700501)
	w.linkTG(t, owner, adaTG)
	key := addPasskey(t, owner, "Phone")
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := w.env.DB.Admin.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The balance, and that the fee is known before paying.
	bal := owner.must(200, "GET", "/v1/payments/balance", nil)
	if bal["available_kobo"] != float64(10_000_000) || bal["quotes_fee"] != true || bal["from"] != "Paystack" || bal["wallet"] != false || bal["add_money"] != nil {
		t.Fatalf("the balance: %v", bal)
	}

	// Musa, a rider on WhatsApp, with the account his payouts go to.
	const musa = "+2348031110071"
	path := strings.TrimPrefix(owner.must(200, "GET", "/v1/riders", nil)["tap_in_url"].(string), "https://"+reachFormHost)
	loc := w.page(t, path+"/whatsapp", nil).Header().Get("Location")
	w.whatsapp(t, musa, strings.TrimPrefix(loc[strings.Index(loc, "text=")+5:], ""))
	rider := owner.must(201, "POST", "/v1/riders", map[string]any{"name": "Musa Bello", "phone": musa, "zones": []string{"Lekki"}, "fee_kobo": 100000})["rider"].(map[string]any)
	if rider["reached"] != "whatsapp" {
		t.Fatalf("musa: %v", rider)
	}
	riderID := uuid.MustParse(rider["id"].(string))
	acct := fitting(t, "044", "076543210")
	rails.names[acct] = "MUSA BELLO"
	if err := db.InTenantTx(ctx, w.env.DB.App, []uuid.UUID{biz}, func(tx pgx.Tx) error {
		_, err := w.srv.Dispatch.SetBankTx(ctx, tx, biz, riderID, dispatch.Bank{BankCode: "044", AccountNumber: acct}, "user:ada", "passkey")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// More than the balance with its fee: stopped, saying by how much.
	if e := owner.must(409, "POST", "/v1/payments", map[string]any{"rider_id": riderID, "amount_kobo": 9_999_000, "purpose": "delivery"}); e["code"] != "short" ||
		!strings.Contains(e["error"].(string), "₦15 short of ₦99,990 + ₦25 fee") {
		t.Fatalf("short: %v", e)
	}

	// ₦45,000 for a delivery: the fee and the balance before confirming.
	x := owner.must(201, "POST", "/v1/payments", map[string]any{"rider_id": riderID, "amount_kobo": 4_500_000, "purpose": "delivery", "where": "taskiem"})["payment"].(map[string]any)
	id := x["id"].(string)
	if x["fee_quote_kobo"] != float64(2500) || x["balance_kobo"] != float64(10_000_000) || x["route"] != "bank" || x["tell_payee"] != true || x["may_tell"] != true {
		t.Fatalf("prepared: %v", x)
	}
	owner.must(400, "POST", "/v1/payments", map[string]any{"rider_id": riderID, "amount_kobo": 100, "where": uuid.NewString()})
	got := owner.must(202, "POST", "/v1/payments/"+id+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+id)})["payment"].(map[string]any)
	if got["status"] != "sending" {
		t.Fatalf("confirmed: %v", got)
	}
	var body string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT payload::text FROM run_events WHERE run_id = $1 AND type = 'RunStarted'`, got["run_id"]).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"route": "bank"`) {
		t.Errorf("the run's route: %s", body)
	}

	// The diary line in the chat it was paid from, naming the payment.
	diary := func() map[string]any {
		t.Helper()
		for _, m := range owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
			if mm := m.(map[string]any); mm["kind"] == "diary" && mm["payment"] == id {
				return mm
			}
		}
		t.Fatal("no diary line")
		return nil
	}
	if d := diary(); d["text"] != "Paid MUSA BELLO ₦45,000 (delivery) · Access Bank ••"+acct[6:] {
		t.Fatalf("the diary line: %v", d)
	}

	// Paid, with its fee: the rider told, the fee its own line.
	run := uuid.MustParse(got["run_id"].(string))
	if _, err := w.srv.PaymentsOps().Finish(ctx, biz, run, uuid.MustParse(id), "paid", "MUSA BELLO", "po_h7", "", ptr(int64(2500)), "bank"); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND kind = 'fee' AND category = 'bank charges' AND amount_kobo = 2500 AND reference = 'po_h7'`, biz); n != 1 {
		t.Fatalf("the fee's own line: %d", n)
	}
	// The payout heard as the bank's event: recorded as a delivery, its
	// fee in its own line, not again on the payout.
	e, ok := moneylog.FromEvent("paystack/main", "payout.settled", map[string]any{"payout_id": "po_h7", "amount_kobo": float64(4_500_000), "fee_kobo": float64(2500)})
	if !ok {
		t.Fatal("no entry")
	}
	if _, _, err := mlog.Record(ctx, biz, e); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND kind = 'payout' AND category = 'delivery' AND fee_kobo IS NULL AND reference = 'po_h7'`, biz); n != 1 {
		t.Fatalf("the payout as a delivery: %d", n)
	}
	if x := owner.must(200, "GET", "/v1/payments/"+id, nil)["payment"].(map[string]any); x["status"] != "paid" || x["fee_kobo"] != float64(2500) {
		t.Fatalf("paid: %v", x)
	}
	w.tick(t)
	told := w.graph.Last(t, musa)
	if !strings.Contains(told.Text, "Ada Foods paid you ₦45,000 for a delivery on") || !strings.Contains(told.Text, "to your account ending "+acct[6:]) ||
		strings.Contains(told.Text, "₦25") || strings.Contains(told.Text, "fee") || strings.Contains(told.Text, "balance") || strings.Contains(told.Text, "Ada Owner") {
		t.Fatalf("the rider's card: %+v", told)
	}
	// Once: finishing it again tells nobody again.
	sent := len(w.graph.Sent(musa))
	if _, err := w.srv.PaymentsOps().Finish(ctx, biz, run, uuid.MustParse(id), "paid", "MUSA BELLO", "po_h7", "", ptr(int64(2500)), "bank"); err != nil {
		t.Fatal(err)
	}
	w.tick(t)
	if len(w.graph.Sent(musa)) != sent {
		t.Fatal("told twice")
	}

	// The receipt: a PDF at a signed link, with no fee or balance.
	rc := owner.must(200, "GET", "/v1/payments/"+id+"/receipt", nil)
	r := rc["receipt"].(map[string]any)
	if r["payee"] != "MUSA BELLO" || r["amount_kobo"] != float64(4_500_000) || r["for"] != "a delivery" || r["business"] != "Ada Foods" || strings.Contains(stringOf(rc), "fee") {
		t.Fatalf("the receipt: %v", rc)
	}
	link := rc["url"].(string)
	rel := link[strings.Index(link, "/v1/"):]
	res, err := http.Get(w.base + rel)
	if err != nil {
		t.Fatal(err)
	}
	pdf, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || !bytes.HasPrefix(pdf, []byte("%PDF")) || res.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("the receipt's link: %d %q", res.StatusCode, pdf[:min(len(pdf), 20)])
	}
	bad := rel[:len(rel)-6] + "A/pdf"
	res2, err := http.Get(w.base + bad)
	if err != nil {
		t.Fatal(err)
	}
	_ = res2.Body.Close()
	if res2.StatusCode != 404 {
		t.Fatalf("a changed link: %d", res2.StatusCode)
	}
	getStatus := func(path string) int {
		t.Helper()
		res, err := http.Get(w.base + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	// The link stops working 90 days after it was paid.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE payments SET finished_at = now() - interval '91 days' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if st := getStatus(rel); st != 404 {
		t.Fatalf("an old receipt's link: %d", st)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE payments SET finished_at = now() WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	// An owner's own payment has no link to send.
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE payments SET personal = true WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if rc := owner.must(200, "GET", "/v1/payments/"+id+"/receipt", nil); rc["url"] != "" {
		t.Fatalf("an owner's own payment's link: %v", rc["url"])
	}
	if st := getStatus(rel); st != 404 {
		t.Fatalf("an owner's own payment by its link: %d", st)
	}
	if _, err := w.env.DB.Admin.Exec(ctx, `UPDATE payments SET personal = false WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	owner.must(201, "POST", "/v1/payments/"+id+"/share", map[string]any{"where": "taskiem"})
	if n := count(`SELECT count(*) FROM chat_history WHERE payment_id = $1 AND kind = 'receipt'`, id); n != 1 {
		t.Fatalf("the receipt in the chat: %d", n)
	}

	// Sent back by the bank: the payment says so, and the rider is told.
	back, _ := moneylog.FromEvent("paystack/main", "payout.reversed", map[string]any{"payout_id": "po_h7", "reason": "Account closed", "fee_kobo": float64(2500)})
	if _, _, err := mlog.Record(ctx, biz, back); err != nil {
		t.Fatal(err)
	}
	if x := owner.must(200, "GET", "/v1/payments/"+id, nil)["payment"].(map[string]any); x["status"] != "reversed" {
		t.Fatalf("reversed: %v", x)
	}
	// The fee came back with it (the rail says so): its line is undone by
	// one naming it; the payout's own line still carries no fee.
	if n := count(`SELECT count(*) FROM money_entries f JOIN money_entries b ON b.corrects = f.id AND b.kind = 'fee' AND b.status = 'reversed' AND b.amount_kobo = 2500
		WHERE f.tenant_id = $1 AND f.kind = 'fee' AND f.status = 'settled' AND f.reference = 'po_h7'`, biz); n != 1 {
		t.Fatalf("the fee's line undone: %d", n)
	}
	if n := count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND kind = 'payout' AND reference = 'po_h7' AND fee_kobo IS NOT NULL`, biz); n != 0 {
		t.Fatalf("a fee on the payout's lines: %d", n)
	}
	// A "paid you" card of this payment (a staff member's, as a person paid
	// sees it) says how it stands now: sent back.
	if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO chat_history (id, tenant_id, user_id, channel, inbound, kind, body, buttons, expires_at, payment_id)
		SELECT gen_random_uuid(), tenant_id, user_id, channel, inbound, 'paid_you', body, buttons, expires_at, payment_id FROM chat_history WHERE payment_id = $1 AND kind = 'diary'`, id); err != nil {
		t.Fatal(err)
	}
	cardStatus := ""
	for _, m := range owner.must(200, "GET", "/v1/taskiem-chat", nil)["messages"].([]any) {
		if mm := m.(map[string]any); mm["kind"] == "paid_you" && mm["payment"] == id {
			cardStatus, _ = mm["payment_status"].(string)
		}
	}
	if cardStatus != "reversed" {
		t.Fatalf("the paid-you card after the bank sent it back: %q", cardStatus)
	}
	w.tick(t)
	if m := w.graph.Last(t, musa); !strings.Contains(m.Text, "did not reach your account") {
		t.Fatalf("told it came back: %+v", m)
	}
	owner.must(409, "GET", "/v1/payments/"+id+"/receipt", nil)
	// Its link stops working once the bank sent it back.
	if st := getStatus(rel); st != 404 {
		t.Fatalf("a reversed payment's receipt link: %d", st)
	}

	// Try again: prepared again as it was asked for, to confirm; the
	// sheet's Tell them in the chat, unticked, is kept as it is confirmed.
	ag := owner.must(201, "POST", "/v1/payments/"+id+"/again", nil)["payment"].(map[string]any)
	if ag["status"] != "prepared" || ag["amount_kobo"] != float64(4_500_000) || ag["rider_id"] != rider["id"] || ag["purpose"] != "delivery" || ag["id"] == id {
		t.Fatalf("again: %v", ag)
	}
	agID := ag["id"].(string)
	owner.must(409, "POST", "/v1/payments/"+agID+"/again", nil)
	owner.must(202, "POST", "/v1/payments/"+agID+"/confirm", map[string]any{"tell_payee": false, "again": true, "passkey": stepUp(t, owner, key, "money.pay", "payment:"+agID)})
	if n := count(`SELECT count(*) FROM payments WHERE id = $1 AND NOT tell_payee AND diary_where = 'taskiem'`, agID); n != 1 {
		t.Fatalf("the tick kept: %d", n)
	}

	// Wages are not told to the person paid.
	wages := owner.must(201, "POST", "/v1/payments", map[string]any{"rider_id": riderID, "amount_kobo": 300_000, "purpose": "wages"})["payment"].(map[string]any)
	if wages["may_tell"] != false {
		t.Fatalf("wages: %v", wages)
	}

	// No fee quote: it still goes, and says the fee shows after paying.
	rails.quotes = false
	nq := owner.must(201, "POST", "/v1/payments", map[string]any{"rider_id": riderID, "amount_kobo": 200_000, "purpose": "delivery"})["payment"].(map[string]any)
	if nq["fee_quote_kobo"] != nil {
		t.Fatalf("no quote: %v", nq)
	}
	// The rail's answer heard before the payment knew its payout: that
	// payout's line carries the fee, so the fee is not written again as a
	// line of its own (never counted twice).
	nqID := nq["id"].(string)
	sent2 := owner.must(202, "POST", "/v1/payments/"+nqID+"/confirm", map[string]any{"passkey": stepUp(t, owner, key, "money.pay", "payment:"+nqID)})["payment"].(map[string]any)
	early, ok := moneylog.FromPayout("paystack/main", map[string]any{"amount_kobo": float64(200_000)}, map[string]any{"payout_id": "po_early", "status": "settled", "fee_kobo": float64(1075)})
	if !ok {
		t.Fatal("no early entry")
	}
	if _, _, err := mlog.Record(ctx, biz, early); err != nil {
		t.Fatal(err)
	}
	if _, err := w.srv.PaymentsOps().Finish(ctx, biz, uuid.MustParse(sent2["run_id"].(string)), uuid.MustParse(nqID), "paid", "MUSA BELLO", "po_early", "", ptr(int64(1075)), "bank"); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM money_entries WHERE tenant_id = $1 AND reference = 'po_early' AND (kind = 'fee' OR fee_kobo IS NOT NULL)`, biz); n != 1 {
		t.Fatalf("the fee counted once: %d", n)
	}

	// Taskiem wallet to Taskiem wallet: Bola Stores' wallet's number is
	// paid wallet to wallet, at once, with no fee; Add money is the
	// wallet's own number.
	bola := w.tenant(t, "Bola Stores", "bola@h7.test")
	bolaID := uuid.MustParse(tenantOf(t, bola))
	bolaAcct := fitting(t, "058", "098765432")
	rails.names[bolaAcct] = "BOLA STORES"
	for _, row := range []struct {
		tenant               uuid.UUID
		wallet, number, bank string
	}{{biz, "w_ada", "1100110011", "Access Bank"}, {bolaID, "w_bola", bolaAcct, "Guaranty Trust Bank"}} {
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO connections (id, tenant_id, environment, connector, name, auth_type, status)
			VALUES ($1, $2, 'prod', 'iswallet', 'taskiem', 'api_key', 'active') ON CONFLICT DO NOTHING`, uuid.New(), row.tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := w.env.DB.Admin.Exec(ctx, `INSERT INTO receive_money (tenant_id, connection_id, mode, wallet_id, owner_type, account_reference, account_number, bank_name, account_name, created_by)
			SELECT $1, id, 'test', $2, 'client_end_business', 'ref', $3, $4, 'Taskiem / Business', 'user:x' FROM connections WHERE tenant_id = $1 AND connector = 'iswallet'`,
			row.tenant, row.wallet, row.number, row.bank); err != nil {
			t.Fatal(err)
		}
	}
	rails.wallet = true
	bal = owner.must(200, "GET", "/v1/payments/balance", nil)
	if bal["wallet"] != true || bal["from"] != "your Taskiem wallet" || bal["add_money"].(map[string]any)["account_number"] != "1100110011" {
		t.Fatalf("the wallet's balance: %v", bal)
	}
	// Bola's wallet number is kept findable (by its bank and number) as
	// Bola's business reads its own wallet.
	bola.must(200, "GET", "/v1/payments/balance", nil)
	var walletKey string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT account_key FROM wallet_accounts WHERE tenant_id = $1`, bolaID).Scan(&walletKey); err != nil || strings.Contains(walletKey, bolaAcct) {
		t.Fatalf("Bola's wallet kept: %q %v", walletKey, err)
	}
	// The same ten digits at another bank are another account: the bank.
	if other := owner.must(201, "POST", "/v1/payments", map[string]any{"account_number": bolaAcct, "bank_name": "Access Bank", "amount_kobo": 500_000, "purpose": "stock"})["payment"].(map[string]any); other["route"] != "bank" {
		t.Fatalf("the same number at another bank: %v", other)
	}
	wx := owner.must(201, "POST", "/v1/payments", map[string]any{"account_number": bolaAcct, "bank_name": "Guaranty Trust Bank", "amount_kobo": 500_000, "purpose": "stock"})["payment"].(map[string]any)
	if wx["route"] != "wallet" || wx["fee_quote_kobo"] != float64(0) || strings.Contains(stringOf(wx), "w_bola") {
		t.Fatalf("wallet to wallet: %v", wx)
	}
	var to string
	if err := w.env.DB.Admin.QueryRow(ctx, `SELECT to_wallet FROM payments WHERE id = $1`, wx["id"]).Scan(&to); err != nil || to != "w_bola" {
		t.Fatalf("the wallet paid: %q %v", to, err)
	}
	// Its own number is not another business's wallet.
	if own := owner.must(201, "POST", "/v1/payments", map[string]any{"account_number": acct, "bank_name": "Access Bank", "amount_kobo": 500_000, "purpose": "stock"})["payment"].(map[string]any); own["route"] != "bank" {
		t.Fatalf("a bank account: %v", own)
	}
	rails.wallet = false

	// Forwarded on WhatsApp: the name checked, and "how much, and for
	// what?"; the answer prepares it with the fee and the balance after.
	rails.quotes = true
	chidi := fitting(t, "058", "012345678")
	rails.names[chidi] = "CHIDI OKEKE"
	waMsg := func(msg map[string]any, wamid string) []whatsapptest.Sent {
		t.Helper()
		before := len(w.graph.Sent(ada))
		body := whatsapptest.Delivery(waPNID, ada, wamid, msg)
		w.post(t, w.wa, body, "X-Hub-Signature-256", whatsapp.Sign(waSecret, body))
		return w.graph.Sent(ada)[before:]
	}
	fwd := whatsapptest.Text("Chidi Okeke\n" + chidi + "\nGTBank")
	fwd["context"] = map[string]any{"forwarded": true}
	out := waMsg(fwd, "wamid.fwd1")
	if len(out) == 0 || !strings.Contains(out[len(out)-1].Text, "Pay CHIDI OKEKE (") || !strings.Contains(out[len(out)-1].Text, "••"+chidi[6:]+") how much, and for what?") {
		t.Fatalf("a forwarded message: %+v", out)
	}
	out = w.whatsapp(t, ada, "45k for a delivery")
	said := ""
	for _, m := range out {
		said += m.Text + "\n"
	}
	if !strings.Contains(said, "Ready to pay ₦45,000 to") || !strings.Contains(said, "₦45,000 + ₦25 fee.") || !strings.Contains(said, "Balance after:") {
		t.Fatalf("prepared from the forward: %q", said)
	}
	// A reply of "pay" quoting a message with account details.
	waMsg(whatsapptest.Text("my account: "+chidi+" GTBank Chidi Okeke"), "wamid.acct1")
	_ = w.whatsapp(t, ada, "cancel")
	reply := whatsapptest.Text("pay")
	reply["context"] = map[string]any{"id": "wamid.acct1"}
	out = waMsg(reply, "wamid.reply1")
	if len(out) == 0 || !strings.Contains(out[len(out)-1].Text, "Pay CHIDI OKEKE (") {
		t.Fatalf("a reply of pay: %+v", out)
	}
	// A reply of "pay" to a message with no account kept (a plan's
	// reminder): read as "pay" is, paying for Taskiem, not a transfer.
	_ = w.whatsapp(t, ada, "cancel")
	plain := w.whatsapp(t, ada, "pay")
	_ = w.whatsapp(t, ada, "cancel")
	reply["context"] = map[string]any{"id": "wamid.nothing"}
	if out := waMsg(reply, "wamid.reply2"); len(out) == 0 || len(plain) == 0 || out[len(out)-1].Text != plain[len(plain)-1].Text ||
		strings.Contains(out[len(out)-1].Text, "how much, and for what") {
		t.Fatalf("a reply quoting nothing kept: %+v, as pay: %+v", out, plain)
	}
	// On Telegram, a reply of "pay" carries the words it quotes.
	tg := w.telegram(t, adaTG, func(id int64) []byte {
		return []byte(fmt.Sprintf(`{"update_id":%d,"message":{"message_id":%d,"from":{"id":%d,"first_name":"Ada"},"chat":{"id":%d,"type":"private"},
			"text":"pay","reply_to_message":{"message_id":3,"chat":{"id":%d,"type":"private"},"text":"Chidi Okeke %s GTBank"}}}`, id, id, adaTG, adaTG, adaTG, chidi))
	})
	if len(tg) == 0 || !strings.Contains(tg[len(tg)-1].Text, "Pay CHIDI OKEKE (") || !strings.Contains(tg[len(tg)-1].Text, "how much, and for what?") {
		t.Fatalf("a reply of pay on Telegram: %+v", tg)
	}
}

func ptr[T any](v T) *T { return &v }
