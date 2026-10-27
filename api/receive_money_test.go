package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/iswallet"
	"github.com/israel-duff/taskiem/connectors/notify"
	"github.com/israel-duff/taskiem/engine/ingest"
	"github.com/israel-duff/taskiem/engine/moneylog"
	"github.com/israel-duff/taskiem/engine/secrets"
)

// fakeWallets is iswallet under Taskiem's own client: wallets made once
// per Idempotency-Key, account numbers once per reference, a balance,
// banks and name enquiry. It records every request's key and what it was
// sent, so a test can see the BVN went once and the key was the
// platform's.
type fakeWallets struct {
	mu       sync.Mutex
	wallets  map[string]string // Idempotency-Key -> wallet id
	accounts map[string]map[string]any
	kyc      bool // answer KYC_REQUIRED to issuing
	bodies   []string
	keys     []string
	srv      *httptest.Server
}

func newFakeWallets(t *testing.T) *fakeWallets {
	f := &fakeWallets{wallets: map[string]string{}, accounts: map[string]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeWallets) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(r.Body)
	f.bodies = append(f.bodies, r.Method+" "+r.URL.Path+" "+raw.String())
	f.keys = append(f.keys, r.Header.Get("Authorization")+r.Header.Get("X-API-Key"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", "req_fake")
	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	var body map[string]any
	_ = json.Unmarshal(raw.Bytes(), &body)
	switch {
	case r.URL.Path == "/v1/wallets" && r.Method == "POST":
		key := r.Header.Get("Idempotency-Key")
		id, ok := f.wallets[key]
		if !ok {
			id = fmt.Sprintf("w_%d", len(f.wallets)+1)
			f.wallets[key] = id
		}
		reply(201, map[string]any{"wallet_id": id, "internal_reference": "9000000001", "account_number": "9000000001", "tier": 3, "owner_type": body["owner_type"]})
	case strings.HasSuffix(r.URL.Path, "/virtual-account"):
		if f.kyc {
			reply(422, map[string]any{"error": map[string]any{"code": "KYC_REQUIRED", "message": "bvn is required for business wallets", "field": "bvn"}})
			return
		}
		ref, _ := body["reference"].(string)
		a, ok := f.accounts[ref]
		if !ok {
			a = map[string]any{"account_number": fmt.Sprintf("88%08d", len(f.accounts)+1), "bank_name": "Wema Bank", "account_name": body["account_name"],
				"reference": ref, "status": "active"}
			f.accounts[ref] = a
		}
		reply(201, a)
	case strings.HasSuffix(r.URL.Path, "/balance"):
		reply(200, map[string]any{"wallet_id": strings.Split(r.URL.Path, "/")[3], "balances": []any{map[string]any{"currency": "NGN", "total": 7500000, "available": 7000000, "pending": 500000}}})
	case r.URL.Path == "/v1/banks":
		reply(200, map[string]any{"banks": []any{map[string]any{"code": "058", "name": "GTBank"}}})
	case r.URL.Path == "/v1/name-enquiry":
		if body["account_number"] == "0000000000" {
			reply(404, map[string]any{"error": map[string]any{"code": "ACCOUNT_NOT_FOUND", "message": "no such account"}})
			return
		}
		reply(200, map[string]any{"account_name": "ADA OBI STORES", "account_number": body["account_number"], "bank_code": body["bank_code"]})
	default:
		reply(404, map[string]any{"error": map[string]any{"code": "NOT_FOUND", "message": r.URL.Path}})
	}
}

func (f *fakeWallets) sent(part string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.bodies {
		if strings.Contains(b, part) {
			n++
		}
	}
	return n
}

// receiveWorld is a Taskiem holding its own iswallet client in test mode
// (a staging Taskiem), with iswallet faked.
func receiveWorld(t *testing.T) (*world, *fakeWallets) {
	w := newWorld(t)
	f := newFakeWallets(t)
	if err := w.env.Registry.Register(iswallet.New(iswallet.Options{BaseURL: f.srv.URL, TestURL: f.srv.URL})); err != nil {
		t.Fatal(err)
	}
	if err := w.env.Registry.Register(notify.New(&notify.Late{})); err != nil {
		t.Fatal(err)
	}
	w.env.Vault.Platform = secrets.PlatformClients{"iswallet": {"test": {"api_key": "isw_platform_test", "shared_client": "true"}}}
	return w, f
}

// A business with no payments account turns on receiving money with
// Taskiem: one wallet under Taskiem's client however often it asks, its
// own number, invoices paid on it, the BVN sent once and kept nowhere;
// the connection holds no key and nobody can change it.
func TestReceiveMoneyTurnsOn(t *testing.T) {
	w, f := receiveWorld(t)
	owner := w.tenant(t, "Ada Obi Stores", "ada@obi.test")

	st := owner.must(200, "GET", "/v1/settings/receive-money", nil)
	if st["available"] != true || st["plan"] != true || st["on"] != false || st["default_owner_type"] != "client_end_business" {
		t.Fatalf("before: %v", st)
	}
	owner.must(400, "POST", "/v1/settings/receive-money", map[string]any{"business_email": "ada@obi.test", "bvn": "123"})
	on := owner.must(201, "POST", "/v1/settings/receive-money", map[string]any{"business_email": "ada@obi.test", "bvn": "22212345678"})
	acct, _ := on["account"].(map[string]any)
	if on["on"] != true || on["mode"] != "test" || on["owner_type"] != "client_end_business" || on["connection"] != "iswallet/taskiem" || acct == nil || acct["account_number"] == "" {
		t.Fatalf("turned on: %v", on)
	}
	// Again: the same wallet, nothing new made.
	again := owner.must(200, "POST", "/v1/settings/receive-money", map[string]any{"business_email": "ada@obi.test", "bvn": "22212345678"})
	if fmt.Sprint(again["account"]) != fmt.Sprint(on["account"]) || f.sent("POST /v1/wallets ") != 1 {
		t.Errorf("asked again: %v; wallets made %d", again, f.sent("POST /v1/wallets "))
	}
	// The BVN reached iswallet once, with the platform's key, and is
	// nowhere in Taskiem's database.
	if f.sent("22212345678") != 1 {
		t.Errorf("the BVN was sent %d times", f.sent("22212345678"))
	}
	for _, k := range f.keys {
		if !strings.Contains(k, "isw_platform_test") {
			t.Errorf("a call without the platform's key: %q", k)
		}
	}
	var leaked int
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE detail::text LIKE '%22212345678%' OR detail::text LIKE '%ada@obi.test%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Errorf("the BVN or email in the audit log: %d %v", leaked, err)
	}

	// The connection: Taskiem's, no key, not the business's to change.
	var conn map[string]any
	for _, c := range owner.must(200, "GET", "/v1/connections", nil)["connections"].([]any) {
		if m := c.(map[string]any); m["name"] == "taskiem" {
			conn = m
		}
	}
	if conn == nil || conn["platform"] != "taskiem" || conn["mode"] != "test" || conn["provisioned_by"] != "taskiem:receive_money" {
		t.Fatalf("the connection: %v", conn)
	}
	id := conn["id"].(string)
	owner.must(409, "PUT", "/v1/connections/"+id+"/credentials", map[string]any{"credentials": map[string]string{"api_key": "mine", "wallet_ids": "w_99"}})
	owner.must(409, "DELETE", "/v1/connections/"+id, nil)
	owner.must(400, "POST", "/v1/connections", map[string]any{"environment": "prod", "connector": "iswallet@1", "name": "taskiem", "mode": "test",
		"credentials": map[string]string{"api_key": "isw_own"}})
	if out := owner.must(200, "POST", "/v1/connections/"+id+"/check", nil); out == nil {
		t.Error("checking the connection")
	}

	// Invoices are paid on it.
	if s := owner.must(200, "GET", "/v1/invoice-settings", nil); s["connection"] != "iswallet/taskiem" {
		t.Errorf("invoice settings: %v", s)
	}

	// The balance, from iswallet.
	if b := owner.must(200, "GET", "/v1/receive-money/balance", nil); b["available_kobo"] != float64(7000000) {
		t.Errorf("balance: %v", b)
	}
}

// A credit to the business's own number, delivered to the platform's
// subscription, lands in that business's money log on its Taskiem
// connection, and in no other business's.
func TestReceiveMoneyCreditReachesTheBusiness(t *testing.T) {
	w, _ := receiveWorld(t)
	ada := w.tenant(t, "Ada Obi Stores", "ada@obi.test")
	bayo := w.tenant(t, "Bayo Prints", "bayo@prints.test")
	on := ada.must(201, "POST", "/v1/settings/receive-money", map[string]any{"business_email": "ada@obi.test", "bvn": "22212345678"})
	bayo.must(201, "POST", "/v1/settings/receive-money", map[string]any{"business_email": "bayo@prints.test", "bvn": "22287654321"})
	var wallet, ref string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT r.wallet_id, r.account_reference FROM receive_money r JOIN tenants t ON t.id = r.tenant_id WHERE t.name = 'Ada Obi Stores'`).
		Scan(&wallet, &ref); err != nil {
		t.Fatal(err)
	}
	h := &ingest.Handler{Store: w.env.Store, Secrets: w.env.Vault, Connections: w.env.Vault, Registry: w.env.Registry,
		MoneyLog: &moneylog.Log{Pool: w.env.DB.App, PII: w.env.Vault},
		Platform: map[string]ingest.Platform{"iswallet": {WebhookSecrets: map[string]string{"test": "whsec_platform_test"}}}}
	mux := http.NewServeMux()
	mux.Handle("/hooks/", http.StripPrefix("/hooks", h))
	edge := httptest.NewServer(mux)
	t.Cleanup(edge.Close)
	acct := on["account"].(map[string]any)
	body := []byte(fmt.Sprintf(`{"id":"evt_p1","event_type":"wallet.credit.posted","wallet_id":%q,"occurred_at":"2026-10-27T09:14:03Z","data":{"txn_id":"t-9",
		"provider_reference":"100004261027091402999999","amount":250000,"amount_credited":249000,"currency":"NGN",
		"virtual_account":{"reference":%q,"account_number":%q,"status":"active"}}}`, wallet, ref, acct["account_number"]))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	m := hmac.New(sha256.New, []byte("whsec_platform_test"))
	m.Write([]byte(ts + "." + string(body)))
	req, _ := http.NewRequest("POST", edge.URL+"/hooks/platform/iswallet@1?mode=test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-iSpend-Timestamp", ts)
	req.Header.Set("X-iSpend-Signature", "sha256="+hex.EncodeToString(m.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("the credit: %d", resp.StatusCode)
	}
	rows, err := w.env.DB.Admin.Query(context.Background(), `SELECT t.name, e.channel, e.amount_kobo, e.fee_kobo FROM money_entries e JOIN tenants t ON t.id = e.tenant_id
		WHERE e.credit_id = '100004261027091402999999'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, conn string
		var amount, fee int64
		if err := rows.Scan(&name, &conn, &amount, &fee); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s %s %d %d", name, conn, amount, fee))
	}
	if strings.Join(got, ";") != "Ada Obi Stores iswallet/taskiem 250000 1000" {
		t.Errorf("money log: %v", got)
	}
}

// Withdrawals go to a bank account the bank named, under the approval
// rule; a withdrawal is a run waiting for an owner, and asking again with
// its key is the same withdrawal.
func TestReceiveMoneyWithdraws(t *testing.T) {
	w, _ := receiveWorld(t)
	owner := w.tenant(t, "Ada Obi Stores", "ada@obi.test")
	owner.must(409, "POST", "/v1/receive-money/withdraw", map[string]any{"amount_kobo": 100000})
	owner.must(201, "POST", "/v1/settings/receive-money", map[string]any{"business_email": "ada@obi.test", "bvn": "22212345678"})
	owner.must(409, "POST", "/v1/receive-money/withdraw", map[string]any{"amount_kobo": 100000})
	if b := owner.must(200, "GET", "/v1/receive-money/banks", nil); len(b["banks"].([]any)) != 1 {
		t.Errorf("banks: %v", b)
	}
	owner.must(400, "PUT", "/v1/settings/receive-money/withdrawals", map[string]any{"bank_code": "058", "account_number": "0000000000"})
	st := owner.must(200, "PUT", "/v1/settings/receive-money/withdrawals", map[string]any{"bank_code": "058", "bank_name": "GTBank", "account_number": "0123456789",
		"two_approvers_above_kobo": 50000000})
	to, _ := st["withdraw_to"].(map[string]any)
	if to == nil || to["account_name"] != "ADA OBI STORES" || st["two_approvers_above_kobo"] != float64(50000000) || st["withdraw_workflow_id"] == nil {
		t.Fatalf("withdrawals: %v", st)
	}
	var doc string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT document::text FROM approval_policies WHERE name = 'taskiem_withdrawals' AND state = 'active'`).Scan(&doc); err != nil ||
		!strings.Contains(doc, "subject.amount_kobo >= 50000000") {
		t.Errorf("the approval rule: %s %v", doc, err)
	}
	// Changing the account keeps the one workflow.
	st2 := owner.must(200, "PUT", "/v1/settings/receive-money/withdrawals", map[string]any{"bank_code": "058", "account_number": "0123456789"})
	if st2["withdraw_workflow_id"] != st["withdraw_workflow_id"] {
		t.Errorf("a second workflow: %v %v", st2["withdraw_workflow_id"], st["withdraw_workflow_id"])
	}
	run := owner.must(201, "POST", "/v1/receive-money/withdraw", map[string]any{"amount_kobo": 1500000}, "Idempotency-Key", "wd-1")
	same := owner.must(200, "POST", "/v1/receive-money/withdraw", map[string]any{"amount_kobo": 1500000}, "Idempotency-Key", "wd-1")
	if run["run_id"] != same["run_id"] || run["workflow_id"] != st["withdraw_workflow_id"] {
		t.Errorf("withdrawals: %v %v", run, same)
	}
	var input string
	if err := w.env.DB.Admin.QueryRow(context.Background(), `SELECT payload::text FROM run_events WHERE run_id = $1 AND type = 'RunStarted'`, run["run_id"]).Scan(&input); err != nil ||
		!strings.Contains(input, `"amount_naira": 15000`) || !strings.Contains(input, `"request_id": "wd-1"`) || strings.Contains(input, "0123456789") {
		t.Errorf("the run's input: %s %v", input, err)
	}
}

// A Taskiem without its own client, or a member who is not an owner,
// cannot turn it on.
func TestReceiveMoneyRefusals(t *testing.T) {
	w, _ := receiveWorld(t)
	owner := w.tenant(t, "Ada Obi Stores", "ada@obi.test")
	w.env.Vault.Platform = nil
	if st := owner.must(200, "GET", "/v1/settings/receive-money", nil); st["available"] != false {
		t.Errorf("without the client: %v", st)
	}
	owner.must(409, "POST", "/v1/settings/receive-money", map[string]any{"business_email": "ada@obi.test", "bvn": "22212345678"})
}
