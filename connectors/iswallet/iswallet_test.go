package iswallet

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
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

func TestNoResendPastTheReplayWindow(t *testing.T) {
	// No exchange is expected: the connector must not call iswallet at all.
	_, err := call(t, "payout", payoutIn, write(3, now.Add(-25*time.Hour)))
	if effects.Classify(err) != effects.KindIndeterminate {
		t.Fatalf("resend after 25h: %v", err)
	}
	// A first attempt with an old key is not a resend (fresh plans use now).
	if _, err := call(t, "payout", payoutIn, write(1, now.Add(-25*time.Hour)), "payout_pending"); err != nil {
		t.Errorf("first attempt: %v", err)
	}
	// A 500 near the window's edge is not trusted either.
	_, err = call(t, "payout", payoutIn, write(1, now.Add(-24*time.Hour)), "payout_internal_error")
	if effects.Classify(err) != effects.KindIndeterminate {
		t.Errorf("500 after 24h: %v", err)
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
