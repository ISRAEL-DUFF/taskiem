package iswallet

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

const key = "tsk_ab12cd34ef56gh78ij90kl12mn34op56"

var now = time.Date(2026, 10, 5, 9, 14, 0, 0, time.UTC)

func call(t *testing.T, action string, input map[string]any, req connector.Request, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL, Now: func() time.Time { return now }})
	req.Input, req.HTTP = input, srv.Client()
	req.Credentials = map[string]string{"api_key": "isw_test_key"}
	return c.Actions[action].Execute(context.Background(), req)
}

func write(attempt int, firstSent time.Time) connector.Request {
	return connector.Request{IdempotencyKey: key, Attempt: attempt, KeyFirstSent: firstSent}
}

var payoutIn = map[string]any{"wallet_id": "6f1e", "amount": 4500000, "narration": "Sept salary", "bank_code": "044",
	"account_number": "0690000031", "account_name": "Ada Obi", "metadata": map[string]any{"run_id": "payroll-2026-09", "employee_id": "emp-417"}}

func TestRegistersWithSandboxHost(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	if c, _ := reg.Get("iswallet@1"); c.Manifest.Hosts()[0] != "synledger.name.ng" {
		t.Errorf("hosts %v", c.Manifest.Hosts())
	}
}

func TestBalanceKeepsEveryCurrencyAndScale(t *testing.T) {
	r, err := call(t, "get_balance", map[string]any{"wallet_id": "6f1e"}, connector.Request{}, "balance")
	if err != nil {
		t.Fatal(err)
	}
	bs := r.Output.(map[string]any)["balances"].([]any)
	ngn, usdt := bs[0].(map[string]any), bs[1].(map[string]any)
	if ngn["available"] != int64(499000000) || ngn["scale"] != 2 || usdt["scale"] != 6 {
		t.Errorf("balances %v", bs)
	}
}

func TestTransferIsFinal(t *testing.T) {
	r, err := call(t, "transfer", map[string]any{"from_wallet_id": "6f1e", "to_wallet_id": "9c2a", "amount": 4500000, "narration": "Sept salary emp-417"},
		write(1, now), "transfer_completed")
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Output.(map[string]any); out["status"] != "completed" || out["txn_id"] != "txn_1" {
		t.Errorf("output %v", out)
	}
}

func TestPayoutSendsKeyAndDestinationNeverFee(t *testing.T) {
	r, err := call(t, "payout", payoutIn, write(1, now), "payout_pending")
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["outflow_id"] != "8d3f" || out["status"] != "pending" || out["idempotency_key"] != key || out["fee"] != float64(5350) {
		t.Errorf("output %v", out)
	}
	// The ambiguous 202: accepted by iswallet, outcome with the bank unknown.
	r, err = call(t, "payout", payoutIn, write(1, now), "payout_ambiguous")
	if err != nil || r.Output.(map[string]any)["provider_reference"] != "" {
		t.Errorf("ambiguous 202: %v %v", r.Output, err)
	}
}

func TestPayoutErrorClassification(t *testing.T) {
	for fx, want := range map[string]effects.ErrorKind{
		"payout_internal_error":      effects.KindUnknownOutcome, // within 24h: resend the same key
		"payout_custody_unavailable": effects.KindRetryable,
		"payout_rate_limited":        effects.KindRetryable,
		"payout_liquidity":           effects.KindRetryable,
		"payout_key_reused":          effects.KindIndeterminate,
		"payout_rejected":            effects.KindFatal,
		"payout_daily_limit":         effects.KindFatal,
		"payout_insufficient":        effects.KindFatal,
	} {
		_, err := call(t, "payout", payoutIn, write(1, now), fx)
		if got := effects.Classify(err); got != want {
			t.Errorf("%s: %v, want %v (%v)", fx, got, want, err)
		}
	}
}

func TestPastTheReplayWindowLooksTheKeyUpFirst(t *testing.T) {
	old := write(3, now.Add(-25*time.Hour))
	// Recorded: answered from the record, nothing is sent again.
	r, err := call(t, "payout", payoutIn, old, "request_outflow_pending")
	if err != nil {
		t.Fatal(err)
	}
	if out := r.Output.(map[string]any); out["outflow_id"] != "8d3f" || out["status"] != "pending" || out["idempotency_key"] != key {
		t.Errorf("recorded payout: %v", out)
	}
	// Not recorded: nothing was posted, so the same key is sent again.
	if _, err := call(t, "payout", payoutIn, old, "request_not_found", "payout_pending"); err != nil {
		t.Errorf("unrecorded payout: %v", err)
	}
	transferIn := map[string]any{"from_wallet_id": "6f1e", "to_wallet_id": "9c2a", "amount": 4500000}
	r, err = call(t, "transfer", transferIn, old, "request_transfer_completed")
	if err != nil || r.Output.(map[string]any)["transfer_id"] != "tr_1" || r.Output.(map[string]any)["status"] != "completed" {
		t.Errorf("recorded transfer: %v %v", r.Output, err)
	}
	// Not yet terminal: wait, never resend.
	if _, err := call(t, "transfer", transferIn, old, "request_transfer_pending"); effects.Classify(err) != effects.KindRetryable {
		t.Errorf("pending transfer: %v", err)
	}
	// A key recorded for another kind of request is for a person to check.
	if _, err := call(t, "transfer", transferIn, old, "request_outflow_pending"); effects.Classify(err) != effects.KindIndeterminate {
		t.Errorf("kind mismatch: %v", err)
	}
	// A first attempt, or a resend within the window, is sent as it is.
	if _, err := call(t, "payout", payoutIn, write(1, now.Add(-25*time.Hour)), "payout_pending"); err != nil {
		t.Errorf("first attempt: %v", err)
	}
	if _, err := call(t, "payout", payoutIn, write(2, now.Add(-time.Hour)), "payout_pending"); err != nil {
		t.Errorf("resend within the window: %v", err)
	}
	// A 500 is an unknown outcome; the next attempt looks the key up.
	if _, err := call(t, "payout", payoutIn, write(3, now.Add(-25*time.Hour)), "request_not_found", "payout_internal_error"); effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("500 after lookup: %v", err)
	}
}

func TestRateLimitWaitsForTheWindowReset(t *testing.T) {
	_, err := call(t, "payout", payoutIn, write(1, now), "payout_rate_limited")
	var ra interface{ RetryAfterDelay() time.Duration }
	if !errors.As(err, &ra) || ra.RetryAfterDelay() != 31*time.Second {
		t.Fatalf("rate limited: %v", err)
	}
}

func TestEventDedupKeepsBothSidesOfATransfer(t *testing.T) {
	m := New(Options{}).Manifest
	e := expr.MustNewTriggerEngine("body", "headers", "query")
	dedup := func(trigger, body string) string {
		t.Helper()
		b, err := expr.DecodeJSON([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		v, err := e.Eval(m.Triggers[trigger].Dedup, map[string]any{"body": b, "headers": map[string]any{}, "query": map[string]any{}})
		if err != nil {
			t.Fatalf("%s: %v", trigger, err)
		}
		return v.(string)
	}
	sender := dedup("transfer_event", `{"id":"evt_1","event_type":"wallet.transfer.completed","wallet_id":"6f1e","data":{"wallet_id":"6f1e","txn_id":"txn_1"}}`)
	receiver := dedup("transfer_event", `{"id":"evt_2","event_type":"wallet.transfer.completed","wallet_id":"9c2a","data":{"wallet_id":"9c2a","txn_id":"txn_1"}}`)
	redelivered := dedup("transfer_event", `{"id":"evt_3","event_type":"wallet.transfer.completed","wallet_id":"6f1e","data":{"wallet_id":"6f1e","txn_id":"txn_1"}}`)
	if sender == receiver || sender != redelivered {
		t.Errorf("sender %q receiver %q redelivered %q", sender, receiver, redelivered)
	}
	if got := dedup("credit_event", `{"id":"evt_4","event_type":"wallet.credit.rejected","wallet_id":"6f1e","data":{}}`); got != "evt_4" {
		t.Errorf("no txn_id: %q", got)
	}
}

func TestLimitsAreFatalWithTheirCode(t *testing.T) {
	_, err := call(t, "transfer", map[string]any{"from_wallet_id": "6f1e", "to_wallet_id": "9c2a", "amount": 25000000}, write(1, now), "transfer_over_limit")
	if effects.Classify(err) != effects.KindFatal || !containsAll(err.Error(), "EXCEEDS_SINGLE_TXN_LIMIT", "req_a1") {
		t.Errorf("limit: %v", err)
	}
}

func TestGetPayoutAndNotFound(t *testing.T) {
	r, err := call(t, "get_payout", map[string]any{"outflow_id": "8d3f"}, connector.Request{}, "get_payout_confirmed")
	if err != nil || r.Output.(map[string]any)["status"] != "confirmed" {
		t.Fatalf("%v %v", r.Output, err)
	}
	_, err = call(t, "get_payout", map[string]any{"outflow_id": "nope"}, connector.Request{}, "get_payout_not_found")
	if !errors.Is(err, connector.ErrNotFound) || effects.Classify(err) != effects.KindFatal {
		t.Errorf("not found: %v", err)
	}
}

func TestNameEnquiry(t *testing.T) {
	r, err := call(t, "name_enquiry", map[string]any{"bank_code": "044", "account_number": "0690000031"}, connector.Request{}, "name_enquiry")
	if err != nil || r.Output.(map[string]any)["account_name"] != "ADA OBI" {
		t.Errorf("%v %v", r, err)
	}
}

func TestWebhookSignature(t *testing.T) {
	m := New(Options{}).Manifest
	spec := m.Triggers["outflow_event"].Verify
	body := []byte(`{"id":"evt_1","event_type":"wallet.outflow.confirmed","data":{"outflow_id":"8d3f","idempotency_key":"` + key + `"}}`)
	sign := func(ts int64, secret string) http.Header {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(strconv.FormatInt(ts, 10) + "." + string(body)))
		h := http.Header{}
		h.Set("X-iSpend-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		h.Set("X-iSpend-Timestamp", strconv.FormatInt(ts, 10))
		return h
	}
	connector.Now = func() time.Time { return now }
	defer func() { connector.Now = time.Now }()
	if err := connector.VerifyWebhook(spec, "whsec", sign(now.Unix(), "whsec"), body); err != nil {
		t.Errorf("valid delivery refused: %v", err)
	}
	if err := connector.VerifyWebhook(spec, "whsec", sign(now.Unix(), "other"), body); err == nil {
		t.Error("wrong secret accepted")
	}
	if err := connector.VerifyWebhook(spec, "whsec", sign(now.Add(-6*time.Minute).Unix(), "whsec"), body); err == nil {
		t.Error("replayed delivery (6 minutes old) accepted")
	}
	tampered := sign(now.Unix(), "whsec")
	if err := connector.VerifyWebhook(spec, "whsec", tampered, append(body, ' ')); err == nil {
		t.Error("tampered body accepted")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, x := range subs {
		if !strings.Contains(s, x) {
			return false
		}
	}
	return true
}

// callAs is call with the connection's other fields (the allow-list).
func callAs(t *testing.T, creds map[string]string, action string, input map[string]any, req connector.Request, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL, Now: func() time.Time { return now }})
	req.Input, req.HTTP = input, srv.Client()
	req.Credentials = map[string]string{"api_key": "isw_test_key"}
	for k, v := range creds {
		req.Credentials[k] = v
	}
	return c.Actions[action].Execute(context.Background(), req)
}

// TestWalletAllowList: a connection with wallet_ids moves and reads money
// only from those wallets; anything else is refused before a request is
// sent (no exchange is served, so a request would fail the fixture).
func TestWalletAllowList(t *testing.T) {
	company := map[string]string{"wallet_ids": "6f1e, 7a00", "shared_client": "true"}
	refused := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrWalletNotAllowed) || effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "WALLET_NOT_ALLOWED") {
			t.Errorf("%s: %v, want a fatal WALLET_NOT_ALLOWED", name, err)
		}
	}
	_, err := callAs(t, company, "transfer", map[string]any{"from_wallet_id": "9c2a", "to_wallet_id": "6f1e", "amount": 100}, write(1, now))
	refused("transfer from another company's wallet", err)
	_, err = callAs(t, company, "payout", map[string]any{"wallet_id": "9c2a", "amount": 100, "bank_code": "044", "account_number": "0690000031", "account_name": "X"}, write(1, now))
	refused("payout from another company's wallet", err)
	_, err = callAs(t, company, "get_balance", map[string]any{"wallet_id": "9c2a"}, connector.Request{})
	refused("balance of another company's wallet", err)
	_, err = callAs(t, company, "transfer", map[string]any{"to_wallet_id": "6f1e", "amount": 100}, write(1, now))
	refused("transfer naming no source", err)

	// Its own wallets work; the destination of a transfer is not limited.
	if _, err := callAs(t, company, "transfer", map[string]any{"from_wallet_id": "6f1e", "to_wallet_id": "9c2a", "amount": 4500000, "narration": "Sept salary emp-417"},
		write(1, now), "transfer_completed"); err != nil {
		t.Errorf("transfer from its own wallet: %v", err)
	}
	if _, err := callAs(t, company, "payout", payoutIn, write(1, now), "payout_pending"); err != nil {
		t.Errorf("payout from its own wallet: %v", err)
	}
	if _, err := callAs(t, company, "get_balance", map[string]any{"wallet_id": "6f1e"}, connector.Request{}, "balance"); err != nil {
		t.Errorf("own balance: %v", err)
	}
	// A payout of another wallet is withheld even when asked for by its id.
	r, err := callAs(t, company, "get_payout", map[string]any{"outflow_id": "8d3f"}, connector.Request{}, "get_payout_confirmed")
	if err != nil || r.Output.(map[string]any)["wallet_id"] != "6f1e" {
		t.Errorf("own payout: %v %v", r.Output, err)
	}
	_, err = callAs(t, map[string]string{"wallet_ids": "7a00"}, "get_payout", map[string]any{"outflow_id": "8d3f"}, connector.Request{}, "get_payout_confirmed")
	refused("another wallet's payout", err)

	// A shared client's connection without wallets may use none.
	_, err = callAs(t, map[string]string{"shared_client": "true"}, "transfer", map[string]any{"from_wallet_id": "6f1e", "to_wallet_id": "9c2a", "amount": 100}, write(1, now))
	if !errors.Is(err, ErrAllowListMissing) || effects.Classify(err) != effects.KindFatal {
		t.Errorf("shared client without wallets: %v", err)
	}
	_, err = callAs(t, map[string]string{"shared_client": "true"}, "get_payout", map[string]any{"outflow_id": "8d3f"}, connector.Request{})
	if !errors.Is(err, ErrAllowListMissing) {
		t.Errorf("shared client without wallets, get_payout: %v", err)
	}
	// A connection of the client's own (no allow-list, not shared) is unchanged.
	if _, err := callAs(t, nil, "get_balance", map[string]any{"wallet_id": "6f1e"}, connector.Request{}, "balance"); err != nil {
		t.Errorf("unlimited connection: %v", err)
	}
}

func TestSharedClientNeedsWallets(t *testing.T) {
	for creds, ok := range map[[2]string]bool{
		{"", ""}: true, {"false", ""}: true, {"true", "6f1e"}: true, {"true", " 6f1e ,7a00"}: true,
		{"true", ""}: false, {"true", " , "}: false, {"yes", "6f1e"}: false,
	} {
		err := New(Options{}).CheckCredentials(map[string]string{"api_key": "k", "shared_client": creds[0], "wallet_ids": creds[1]})
		if (err == nil) != ok {
			t.Errorf("shared_client %q wallet_ids %q: %v", creds[0], creds[1], err)
		}
	}
}

func TestEventsRouteByWallet(t *testing.T) {
	m := New(Options{}).Manifest
	for _, name := range []string{"outflow_event", "credit_event", "transfer_event"} {
		if r := m.Triggers[name].Route; r == nil || r.Key != "=body.wallet_id" || r.Field != "wallet_ids" {
			t.Errorf("%s route %+v", name, r)
		}
	}
	if k := m.RouteKeys(map[string]string{"wallet_ids": "6f1e,7a00, 6f1e"}); len(k["wallet_ids"]) != 2 {
		t.Errorf("route keys %v", k)
	}
}

func TestCapabilities(t *testing.T) {
	fixture.Capabilities(t, func(base string) *connector.Connector { return New(Options{BaseURL: base}) })
}

func TestModesChooseTheHost(t *testing.T) {
	var hits []string
	serve := func(name string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits = append(hits, name+" "+r.URL.Path)
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	live, test := serve("live"), serve("test")
	ask := func(c *connector.Connector, mode string) error {
		creds := map[string]string{"api_key": "isw_key"}
		if mode != "" {
			creds[connector.ModeKey] = mode
		}
		_, err := c.Actions["list_banks"].Execute(context.Background(), connector.Request{Input: map[string]any{}, Credentials: creds, HTTP: http.DefaultClient})
		return err
	}
	c := New(Options{LiveURL: live.URL, TestURL: test.URL, BaseURL: live.URL})
	for _, mode := range []string{"live", "test", ""} {
		if err := ask(c, mode); err != nil {
			t.Fatalf("%q: %v", mode, err)
		}
	}
	if strings.Join(hits, ", ") != "live /v1/banks, test /v1/banks, live /v1/banks" {
		t.Errorf("hosts: %v", hits)
	}
	// Before go-live there is no live address: nothing is sent.
	hits = nil
	if err := ask(New(Options{}), "live"); !errors.Is(err, effects.ErrFatal) || len(hits) > 0 {
		t.Errorf("live with no address: %v, %v", err, hits)
	}
	if _, ok := New(Options{}).Manifest.ModeURL("test"); !ok {
		t.Error("the sandbox is not test's address by default")
	}
}
