package interswitch

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

const ref = "tsk_ab12cd34ef56gh78ij90kl12mn34op56"

var creds = map[string]string{"client_id": "IKIATASKIEM0001", "client_secret": "tsk-isw-secret", "wallet_id": "2700007687",
	"wallet_pin": "1234", "merchant_code": "MX6072", "webhook_secret": "whsec-isw"}

func call(t *testing.T, action string, input map[string]any, attempt int, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), IdempotencyKey: ref, Attempt: attempt, Credentials: creds})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

var payIn = map[string]any{"amount": 150050, "account_number": "0037320662", "bank_code": "TRP", "narration": "Sept salary", "reference": ref}

func TestRegistersEveryHost(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("interswitch@1")
	if !ok {
		t.Fatal("interswitch@1 not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 6 {
		t.Errorf("hosts %v", h)
	}
	cl := &client{live: Live, sandbox: Sandbox}
	if cl.hosts(connector.Request{Credentials: map[string]string{"environment": "SANDBOX"}}).Payouts != "https://payouts-sandbox.interswitchng.com" ||
		cl.hosts(connector.Request{}).Webpay != "https://webpay.interswitchng.com" {
		t.Error("environment selection")
	}
	if h := New(Options{BaseURL: "http://127.0.0.1:9"}).Manifest.Hosts(); len(h) != 1 || h[0] != "127.0.0.1" {
		t.Errorf("override hosts %v", h)
	}
}

func TestTokenIsCachedRenewedAndRefreshedOn401(t *testing.T) {
	srv := fixture.Serve(t, fixture.Load(t, "token"), fixture.Load(t, "banks"), fixture.Load(t, "banks"),
		fixture.Load(t, "token2"), fixture.Load(t, "banks_tok2"))
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	c := newConnector(Options{BaseURL: srv.URL}, func() time.Time { return now })
	req := connector.Request{HTTP: srv.Client(), Credentials: creds, Attempt: 1}
	for i := 0; i < 2; i++ {
		if r, err := c.Actions["list_banks"].Execute(context.Background(), req); err != nil || len(out(r)["banks"].([]any)) != 2 {
			t.Fatalf("call %d: %v %v", i, r.Output, err)
		}
	}
	now = now.Add(24 * time.Hour) // expires_in is a day
	if _, err := c.Actions["list_banks"].Execute(context.Background(), req); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	// A revoked token: one 401, a new token, the call again. On a write
	// too: a 401 means Interswitch refused before acting.
	if _, err := call(t, "list_banks", nil, 1, "token", "banks_revoked", "token2", "banks_tok2"); err != nil {
		t.Errorf("401 refresh: %v", err)
	}
	if r, err := call(t, "transfer", payIn, 1, "token", "payout_revoked", "token2", "payout_successful_tok2"); err != nil || out(r)["status"] != "SUCCESSFUL" {
		t.Errorf("401 refresh on payout: %v %v", r.Output, err)
	}
}

func TestPassportFailures(t *testing.T) {
	_, err := call(t, "list_banks", nil, 1, "token_refused")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Bad client credentials") {
		t.Errorf("refused: %v", err)
	}
	// Passport down: nothing was attempted, so the step may retry.
	_, err = call(t, "transfer", payIn, 1, "token_down")
	if effects.Classify(err) != effects.KindRetryable {
		t.Errorf("passport 502: %v", err)
	}
	_, err = New(Options{BaseURL: "https://x.test"}).Actions["list_banks"].Execute(context.Background(), connector.Request{Credentials: map[string]string{"client_id": "x"}})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("no secret: %v", err)
	}
}

func TestReads(t *testing.T) {
	r, err := call(t, "list_banks", map[string]any{"name": "GUA"}, 1, "token", "banks_filtered")
	if b := out(r)["banks"].([]any); err != nil || len(b) != 1 || b[0].(map[string]any)["cbn_code"] != "058" {
		t.Errorf("banks %v %v", r.Output, err)
	}
	r, err = call(t, "resolve_account", map[string]any{"account_number": "0037320662", "bank_code": "TRP"}, 1, "token", "lookup")
	if err != nil || out(r)["account_name"] != "JAMES MODUPE" {
		t.Errorf("lookup %v %v", r.Output, err)
	}
	_, err = call(t, "resolve_account", map[string]any{"account_number": "0037320662", "bank_code": "TRP"}, 1, "token", "lookup_no_name")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Unable to locate record") {
		t.Errorf("no name: %v", err)
	}
	_, err = call(t, "resolve_account", map[string]any{"account_number": "0037320662", "bank_code": "TRP"}, 1, "token", "lookup_invalid")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Invalid account number") {
		t.Errorf("invalid: %v", err)
	}
	if a, b := lookupRef(), lookupRef(); a == b || !strings.HasPrefix(a, "tsk-lookup-") {
		t.Error("lookup references must be fresh")
	}
}

func TestPayoutSendsNaira(t *testing.T) {
	in := map[string]any{"source_account_name": "Acme Ltd", "source_account_number": "0012345678"}
	for k, v := range payIn {
		in[k] = v
	}
	r, err := call(t, "transfer", in, 1, "token", "payout_successful")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "SUCCESSFUL" || o["amount"] != int64(150050) || o["fee"] != int64(1000) || o["reference"] != ref ||
		o["payout_id"] != "102865" || o["account_name"] != "JAMES MODUPE" {
		t.Errorf("output %v", o)
	}
	r, err = call(t, "transfer", payIn, 1, "token", "payout_processing")
	if err != nil || out(r)["status"] != "PROCESSING" {
		t.Errorf("processing %v %v", r.Output, err)
	}
	r, err = call(t, "transfer", payIn, 1, "token", "payout_processing_202")
	if err != nil || out(r)["status"] != "PROCESSING" || out(r)["response_code"] != "09" {
		t.Errorf("202 processing %v %v", r.Output, err)
	}
}

func TestResendAsksFirst(t *testing.T) {
	// The first attempt reached Interswitch: the resend reports it.
	r, err := call(t, "transfer", payIn, 2, "token", "get_successful")
	if err != nil || out(r)["status"] != "SUCCESSFUL" {
		t.Fatalf("resend after success: %v %v", r.Output, err)
	}
	_, err = call(t, "transfer", payIn, 2, "token", "get_failed")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Terminal failure") {
		t.Errorf("resend after failure: %v", err)
	}
	// It did not: the resend sends.
	if r, err := call(t, "transfer", payIn, 2, "token", "get_not_found", "payout_processing"); err != nil || out(r)["status"] != "PROCESSING" {
		t.Errorf("resend after nothing: %v %v", r.Output, err)
	}
	// The check itself fails: do not send blind.
	_, err = call(t, "transfer", payIn, 2, "token", "get_server_error")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("check failed: %v", err)
	}
}

func TestDuplicateReportsTheOriginal(t *testing.T) {
	for _, refusal := range []string{"payout_duplicate", "payout_conflict"} {
		r, err := call(t, "transfer", payIn, 1, "token", refusal, "get_processing")
		if err != nil || out(r)["status"] != "PROCESSING" {
			t.Errorf("%s then found: %v %v", refusal, r.Output, err)
		}
		_, err = call(t, "transfer", payIn, 1, "token", refusal, "get_not_found")
		if effects.Classify(err) != effects.KindUnknownOutcome {
			t.Errorf("%s, not listed: %v", refusal, err)
		}
	}
}

func TestPayoutOutcomes(t *testing.T) {
	_, err := call(t, "transfer", payIn, 1, "token", "payout_failed")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Insufficient Funds") {
		t.Errorf("failed: %v", err)
	}
	_, err = call(t, "transfer", payIn, 1, "token", "payout_failed_400")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Wallet Not Found") {
		t.Errorf("53: %v", err)
	}
	_, err = call(t, "transfer", payIn, 1, "token", "payout_bad_request")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "narration must not be blank") {
		t.Errorf("400: %v", err)
	}
	for _, f := range []string{"payout_server_error", "payout_gateway_timeout", "payout_unreadable", "payout_odd_status"} {
		if _, err := call(t, "transfer", payIn, 1, "token", f); effects.Classify(err) != effects.KindUnknownOutcome {
			t.Errorf("%s: %v", f, err)
		}
	}
	for _, in := range []map[string]any{
		{"amount": 100.5, "account_number": "1", "bank_code": "TRP", "narration": "x", "reference": ref},
		{"amount": 100, "narration": "x", "reference": ref},
		{"amount": 100, "account_number": "1", "bank_code": "TRP", "narration": "x"},
	} {
		if _, err := call(t, "transfer", in, 1); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", in, err)
		}
	}
	noPin := New(Options{BaseURL: "https://x.test"})
	_, err = noPin.Actions["transfer"].Execute(context.Background(), connector.Request{Input: payIn, Attempt: 1,
		Credentials: map[string]string{"client_id": "a", "client_secret": "b", "wallet_id": "w"}})
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "wallet_pin") {
		t.Errorf("no pin: %v", err)
	}
}

func TestGetTransfer(t *testing.T) {
	r, err := call(t, "get_transfer", map[string]any{"reference": ref}, 1, "token", "get_failed")
	if err != nil || out(r)["status"] != "FAILED" || out(r)["reversed"] != true {
		t.Errorf("failed read: %v %v", r.Output, err)
	}
	_, err = call(t, "get_transfer", map[string]any{"reference": ref}, 1, "token", "get_not_found")
	if !errors.Is(err, connector.ErrNotFound) || effects.Classify(err) != effects.KindFatal {
		t.Errorf("not found: %v", err)
	}
}

func TestGetPayment(t *testing.T) {
	in := map[string]any{"reference": "order-17", "amount": 150050}
	r, err := call(t, "get_payment", in, 1, "payment_approved")
	if err != nil || out(r)["status"] != "successful" || out(r)["amount_matches"] != true || out(r)["amount"] != int64(150050) {
		t.Errorf("approved %v %v", r.Output, err)
	}
	// Approved, but not what was asked: never give value on this.
	r, err = call(t, "get_payment", in, 1, "payment_short")
	if err != nil || out(r)["amount_matches"] != false {
		t.Errorf("short %v %v", r.Output, err)
	}
	if r, err := call(t, "get_payment", in, 1, "payment_pending"); err != nil || out(r)["status"] != "pending" {
		t.Errorf("pending %v %v", r.Output, err)
	}
	if r, err := call(t, "get_payment", in, 1, "payment_declined"); err != nil || out(r)["status"] != "failed" {
		t.Errorf("declined %v %v", r.Output, err)
	}
	for _, f := range []string{"payment_not_found", "payment_not_found_404"} {
		if _, err := call(t, "get_payment", in, 1, f); !errors.Is(err, connector.ErrNotFound) || effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s: %v", f, err)
		}
	}
	if _, err := call(t, "get_payment", in, 1, "payment_unreadable"); effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("unreadable: %v", err)
	}
	if _, err := call(t, "get_payment", map[string]any{"reference": "order-17"}, 1); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no amount: %v", err)
	}
}

func TestWebhookSignature(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"].Verify
	body := []byte(`{"event": "TRANSACTION.UPDATED", "uuid": "2Xdf35faAyX2Sk5Dalu405rUD", "timestamp":1594646111460,"data":{ "bankCode":"011"}}`)
	m := hmac.New(sha512.New, []byte(creds["webhook_secret"]))
	m.Write(body)
	h := http.Header{}
	h.Set("X-Interswitch-Signature", hex.EncodeToString(m.Sum(nil)))
	if err := connector.VerifyWebhook(spec, creds["webhook_secret"], h, body); err != nil {
		t.Errorf("valid: %v", err)
	}
	h.Set("X-Interswitch-Signature", strings.ToUpper(h.Get("X-Interswitch-Signature")))
	if err := connector.VerifyWebhook(spec, creds["webhook_secret"], h, body); err != nil {
		t.Errorf("upper-case hex: %v", err)
	}
	if connector.VerifyWebhook(spec, "other", h, body) == nil {
		t.Error("wrong secret accepted")
	}
	if connector.VerifyWebhook(spec, creds["webhook_secret"], h, append(body, ' ')) == nil {
		t.Error("tampered body accepted")
	}
	if connector.VerifyWebhook(spec, creds["webhook_secret"], http.Header{}, body) == nil {
		t.Error("unsigned delivery accepted")
	}
}

func TestTriggerExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"]
	e := expr.MustNewWithRoots("body", "headers", "query")
	for _, c := range []struct{ body, event, dedup, correlation string }{
		{`{"event":"TRANSACTION.COMPLETED","uuid":"2Xdf35faAyX2Sk5Dalu405rUD","timestamp":1594646111460,"data":{"amount":12000,"responseCode":"00","merchantReference":"order-17","paymentReference":"FBN|WEB|MX6072|13-07-2020|3481032|762672"}}`,
			"TRANSACTION.COMPLETED", "TRANSACTION.COMPLETED:2Xdf35faAyX2Sk5Dalu405rUD:1594646111460", "order-17"},
		{`{"event":"TRANSACTION.UPDATED","uuid":"u2","timestamp":1594646111999,"data":{"merchantReference":null}}`,
			"TRANSACTION.UPDATED", "TRANSACTION.UPDATED:u2:1594646111999", "u2"},
		{`{"event":"INVOICE.TRANSACTION_SUCCESSFUL","uuid":"inv-9","timestamp":1594646112000,"data":{"amount":5000}}`,
			"INVOICE.TRANSACTION_SUCCESSFUL", "INVOICE.TRANSACTION_SUCCESSFUL:inv-9:1594646112000", "inv-9"},
		{`{"event":"LINK.TRANSACTION_FAILURE","uuid":"lnk-1","data":{}}`,
			"LINK.TRANSACTION_FAILURE", "LINK.TRANSACTION_FAILURE:lnk-1:", "lnk-1"},
	} {
		body, err := expr.DecodeJSON([]byte(c.body))
		if err != nil {
			t.Fatal(err)
		}
		act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}}
		for _, f := range []struct{ name, src, want string }{{"event_type", spec.EventType, c.event}, {"dedup", spec.Dedup, c.dedup}, {"correlation", spec.Correlation, c.correlation}} {
			v, err := e.Eval(f.src, act)
			if err != nil {
				t.Errorf("%s %s: %v", c.event, f.name, err)
				continue
			}
			if got, _ := v.(string); got != f.want {
				t.Errorf("%s %s = %v, want %q", c.event, f.name, v, f.want)
			}
		}
	}
}
