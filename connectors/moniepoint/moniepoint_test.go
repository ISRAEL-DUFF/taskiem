package moniepoint

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

var creds = map[string]string{"api_key": "MK_TEST_TASKIEM01", "secret_key": "TSKSECRET0000000000000000000001",
	"contract_code": "8389328412", "wallet_account_number": "8016472829"}

// call runs one action against a fresh connector (so a fresh login).
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

var transferIn = map[string]any{"amount": 200000, "account_number": "2085096393", "bank_code": "057", "narration": "Sept salary", "reference": ref}

func TestRegistersBothHosts(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("moniepoint@1")
	if !ok {
		t.Fatal("moniepoint@1 not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "api.monnify.com" || h[1] != "sandbox.monnify.com" {
		t.Errorf("hosts %v", h)
	}
	cl := &client{live: "L", sandbox: "S"}
	if cl.base(connector.Request{Credentials: map[string]string{"environment": " Sandbox"}}) != "S" || cl.base(connector.Request{}) != "L" {
		t.Error("environment selection")
	}
}

func TestTokenIsCachedRenewedAndRefreshedOn401(t *testing.T) {
	srv := fixture.Serve(t,
		fixture.Load(t, "login"), fixture.Load(t, "banks"), fixture.Load(t, "banks"), // one login serves two calls
		fixture.Load(t, "login2"), fixture.Load(t, "banks_tok2"), // expired: a new login
	)
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	c := newConnector(Options{BaseURL: srv.URL}, func() time.Time { return now })
	banks := c.Actions["list_banks"]
	req := connector.Request{HTTP: srv.Client(), Credentials: creds, Attempt: 1}
	for i := 0; i < 2; i++ {
		r, err := banks.Execute(context.Background(), req)
		if err != nil || len(out(r)["banks"].([]any)) != 2 {
			t.Fatalf("call %d: %v %v", i, r.Output, err)
		}
	}
	now = now.Add(59 * time.Minute) // past expiresIn less the minute's margin
	if _, err := banks.Execute(context.Background(), req); err != nil {
		t.Fatalf("after expiry: %v", err)
	}

	// Monnify revokes a token early: one 401, a new login, the call again.
	srv2 := fixture.Serve(t, fixture.Load(t, "login"), fixture.Load(t, "banks_expired_token"), fixture.Load(t, "login2"), fixture.Load(t, "banks_tok2"))
	c2 := New(Options{BaseURL: srv2.URL})
	if _, err := c2.Actions["list_banks"].Execute(context.Background(), connector.Request{HTTP: srv2.Client(), Credentials: creds, Attempt: 1}); err != nil {
		t.Errorf("401 refresh: %v", err)
	}
}

func TestLoginRefusedIsFatal(t *testing.T) {
	_, err := call(t, "list_banks", nil, 1, "login_refused")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Invalid client credentials") {
		t.Errorf("refused login: %v", err)
	}
	c := New(Options{BaseURL: "https://example.test"})
	_, err = c.Actions["list_banks"].Execute(context.Background(), connector.Request{Credentials: map[string]string{"api_key": "x"}})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("no secret: %v", err)
	}
}

func TestReads(t *testing.T) {
	r, err := call(t, "get_balance", nil, 1, "login", "balance")
	if err != nil || out(r)["available_balance"] != int64(37893) || out(r)["ledger_balance"] != int64(40050) {
		t.Errorf("balance %v %v", r.Output, err)
	}
	// Half a kobo cannot be read exactly: an error, never a guess.
	_, err = call(t, "get_balance", nil, 1, "login", "balance_unreadable")
	if effects.Classify(err) != effects.KindUnknownOutcome || !errors.Is(err, effects.ErrUnknownOutcome) {
		t.Errorf("unreadable balance: %v", err)
	}
	r, err = call(t, "resolve_account", map[string]any{"account_number": "0068687503", "bank_code": "232"}, 1, "login", "validate")
	if err != nil || out(r)["account_name"] != "ADA NGOZI OBI" {
		t.Errorf("resolve %v %v", r.Output, err)
	}
	_, err = call(t, "resolve_account", map[string]any{"account_number": "0068687503", "bank_code": "232"}, 1, "login", "validate_invalid")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Invalid account number") {
		t.Errorf("invalid account: %v", err)
	}
}

func TestTransferSendsNaira(t *testing.T) {
	r, err := call(t, "transfer", transferIn, 1, "login", "transfer_success")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "SUCCESS" || o["amount"] != int64(200000) || o["fee"] != int64(1075) || o["session_id"] == "" || o["requires_authorization"] != false {
		t.Errorf("output %v", o)
	}
	named := map[string]any{"amount": 200050, "account_number": "2085096393", "bank_code": "057", "account_name": "Ada Obi", "narration": "Sept salary", "async": true, "reference": ref}
	r, err = call(t, "transfer", named, 1, "login", "transfer_pending_async")
	if err != nil || out(r)["status"] != "PENDING" || out(r)["amount"] != int64(200050) {
		t.Errorf("async %v %v", r.Output, err)
	}
	r, err = call(t, "transfer", transferIn, 1, "login", "transfer_needs_otp")
	if err != nil || out(r)["requires_authorization"] != true {
		t.Errorf("2FA %v %v", r.Output, err)
	}
}

func TestRepeatReportsTheOriginalTransfer(t *testing.T) {
	// A resend after a lost response: D05, then the transfer under it.
	r, err := call(t, "transfer", transferIn, 2, "login", "transfer_duplicate", "summary_success")
	if err != nil || out(r)["status"] != "SUCCESS" || out(r)["transaction_reference"] != "MFDS20261006091338AAAAFE" || out(r)["amount"] != int64(200000) {
		t.Fatalf("duplicate then found: %v %v", r.Output, err)
	}
	// The same refusal inside a 200.
	if _, err := call(t, "transfer", transferIn, 2, "login", "transfer_duplicate_200", "summary_success"); err != nil {
		t.Errorf("duplicate in 200: %v", err)
	}
	// The original failed: nothing moved; the step fails with the reason.
	_, err = call(t, "transfer", transferIn, 2, "login", "transfer_duplicate", "summary_failed")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Insufficient wallet balance") {
		t.Errorf("failed original: %v", err)
	}
	_, err = call(t, "transfer", transferIn, 2, "login", "transfer_duplicate", "summary_reversed")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "reversed") {
		t.Errorf("reversed original: %v", err)
	}
	// Refused, yet not listed: never assume it did not happen.
	_, err = call(t, "transfer", transferIn, 2, "login", "transfer_duplicate", "summary_not_found")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("refused, not listed: %v", err)
	}
	// D07 (same account and amount within two minutes): ours if listed.
	if r, err := call(t, "transfer", transferIn, 2, "login", "transfer_recent_repeat", "summary_success"); err != nil || out(r)["status"] != "SUCCESS" {
		t.Errorf("D07 ours: %v %v", r.Output, err)
	}
	_, err = call(t, "transfer", transferIn, 1, "login", "transfer_recent_repeat", "summary_not_found")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "nothing was sent") {
		t.Errorf("D07 not ours: %v", err)
	}
}

func TestTransferErrors(t *testing.T) {
	_, err := call(t, "transfer", transferIn, 1, "login", "transfer_failed")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "sufficient balance") {
		t.Errorf("failed: %v", err)
	}
	_, err = call(t, "transfer", transferIn, 1, "login", "transfer_insufficient")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "D04") {
		t.Errorf("D04: %v", err)
	}
	// Code 99: "Re-query to ascertain transaction status".
	_, err = call(t, "transfer", transferIn, 1, "login", "transfer_unexpected")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("99: %v", err)
	}
	_, err = call(t, "transfer", transferIn, 1, "login", "transfer_server_error")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("502: %v", err)
	}
	_, err = call(t, "transfer", transferIn, 1, "login", "transfer_unavailable")
	if effects.Classify(err) != effects.KindRetryable {
		t.Errorf("503: %v", err)
	}
	_, err = call(t, "transfer", transferIn, 1, "login", "transfer_unreadable")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("unreadable amount: %v", err)
	}
	// Fractional kobo and a missing destination never reach Monnify.
	for _, in := range []map[string]any{
		{"amount": 100.5, "account_number": "1", "bank_code": "1", "narration": "x", "reference": ref},
		{"amount": 100, "narration": "x", "reference": ref},
		{"amount": 100, "account_number": "1", "bank_code": "1", "narration": "x"},
	} {
		if _, err := call(t, "transfer", in, 1); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v: %v", in, err)
		}
	}
}

func TestGetTransfer(t *testing.T) {
	r, err := call(t, "get_transfer", map[string]any{"reference": ref}, 1, "login", "summary_failed")
	if err != nil || out(r)["status"] != "FAILED" || out(r)["reason"] != "Insufficient wallet balance" {
		t.Errorf("failed read: %v %v", r.Output, err)
	}
	_, err = call(t, "get_transfer", map[string]any{"reference": ref}, 1, "login", "summary_not_found")
	if !errors.Is(err, connector.ErrNotFound) || effects.Classify(err) != effects.KindFatal {
		t.Errorf("not found: %v", err)
	}
}

var reserveIn = map[string]any{"account_name": "Acme / Ada Obi", "customer_email": "ada@example.com", "customer_name": "Ada Obi",
	"bvn": "21212121212", "preferred_banks": []any{"50515"}, "account_reference": ref}

func TestReservedAccounts(t *testing.T) {
	r, err := call(t, "create_reserved_account", reserveIn, 1, "login", "reserve")
	if err != nil {
		t.Fatal(err)
	}
	a := out(r)["accounts"].([]any)[0].(map[string]any)
	if out(r)["account_reference"] != ref || a["account_number"] != "6254727989" || a["bank_code"] != "50515" {
		t.Errorf("reserve %v", r.Output)
	}
	// A refusal naming the reference: report the account already reserved.
	r, err = call(t, "create_reserved_account", reserveIn, 1, "login", "reserve_dup", "reserved_details")
	if err != nil || out(r)["accounts"].([]any)[0].(map[string]any)["account_name"] != "Acme / Ada Obi" {
		t.Errorf("duplicate: %v %v", r.Output, err)
	}
	_, err = call(t, "create_reserved_account", reserveIn, 1, "login", "reserve_dup", "reserved_not_found")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("refused, not listed: %v", err)
	}
	// A resend asks first, and sends only if nothing was made.
	if r, err := call(t, "create_reserved_account", reserveIn, 2, "login", "reserved_details"); err != nil || out(r)["status"] != "ACTIVE" {
		t.Errorf("resend found: %v %v", r.Output, err)
	}
	if _, err := call(t, "create_reserved_account", reserveIn, 2, "login", "reserved_not_found", "reserve"); err != nil {
		t.Errorf("resend after nothing: %v", err)
	}
	_, err = call(t, "get_reserved_account", map[string]any{"account_reference": ref}, 1, "login", "reserved_not_found")
	if !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("get not found: %v", err)
	}
	noContract := New(Options{BaseURL: "https://example.test"})
	_, err = noContract.Actions["create_reserved_account"].Execute(context.Background(), connector.Request{Input: reserveIn,
		Credentials: map[string]string{"api_key": "k", "secret_key": "s"}})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("no contract code: %v", err)
	}
}

var initIn = map[string]any{"amount": 150050, "customer_name": "Ada Obi", "customer_email": "ada@example.com", "description": "Order 17",
	"redirect_url": "https://shop.example.com/done", "payment_methods": []any{"CARD", "ACCOUNT_TRANSFER"}, "payment_reference": ref}

func TestCheckout(t *testing.T) {
	r, err := call(t, "init_transaction", initIn, 1, "login", "init")
	if err != nil || !strings.HasPrefix(str(out(r), "checkout_url"), "https://") || out(r)["status"] != "PENDING" {
		t.Errorf("init %v %v", r.Output, err)
	}
	r, err = call(t, "init_transaction", initIn, 1, "login", "init_duplicate", "query_pending")
	if err != nil || out(r)["status"] != "PENDING" || out(r)["checkout_url"] != "" || out(r)["transaction_reference"] == "" {
		t.Errorf("duplicate: %v %v", r.Output, err)
	}
	if r, err := call(t, "init_transaction", initIn, 2, "login", "query_paid"); err != nil || out(r)["status"] != "PAID" {
		t.Errorf("resend found: %v %v", r.Output, err)
	}
	if _, err := call(t, "init_transaction", initIn, 2, "login", "query_not_found", "init"); err != nil {
		t.Errorf("resend after nothing: %v", err)
	}
}

func TestGetTransaction(t *testing.T) {
	r, err := call(t, "get_transaction", map[string]any{"payment_reference": ref}, 1, "login", "query_paid")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "PAID" || o["amount_paid"] != int64(150050) || o["settlement_amount"] != int64(148921) || o["product_reference"] != ref {
		t.Errorf("paid %v", o)
	}
	r, err = call(t, "get_transaction", map[string]any{"transaction_reference": "MNFY|20261006200044|000090"}, 1, "login", "query_by_txn")
	if err != nil || out(r)["product_type"] != "RESERVED_ACCOUNT" || out(r)["product_reference"] != "acct-ref-1" {
		t.Errorf("by transaction reference %v %v", r.Output, err)
	}
	r, err = call(t, "get_transaction", map[string]any{"payment_reference": ref}, 1, "login", "query_pending")
	if err != nil || out(r)["amount_paid"] != int64(0) || out(r)["settlement_amount"] != int64(0) {
		t.Errorf("pending %v %v", r.Output, err)
	}
	_, err = call(t, "get_transaction", map[string]any{"payment_reference": ref}, 1, "login", "query_not_found")
	if !errors.Is(err, connector.ErrNotFound) || effects.Classify(err) != effects.KindFatal {
		t.Errorf("not found: %v", err)
	}
	_, err = call(t, "get_transaction", map[string]any{"payment_reference": ref}, 1, "login", "query_unreadable")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("unreadable: %v", err)
	}
	if _, err := call(t, "get_transaction", map[string]any{}, 1); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no reference: %v", err)
	}
}

func TestWebhookSignature(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"].Verify
	// Monnify's own worked example (Computing Request Validation Hash).
	body := []byte(`{"eventData":{"product":{"reference":"111222333","type":"OFFLINE_PAYMENT_AGENT"},"transactionReference":"MNFY|76|20211117154810|000001","paymentReference":"0.01462001097368737","paidOn":"17/11/2021 3:48:10 PM","paymentDescription":"Mockaroo Jesse","metaData":{},"destinationAccountInformation":{},"paymentSourceInformation":{},"amountPaid":78000,"totalPayable":78000,"offlineProductInformation":{"code":"41470","type":"DYNAMIC"},"cardDetails":{},"paymentMethod":"CASH","currency":"NGN","settlementAmount":77600,"paymentStatus":"PAID","customer":{"name":"Mockaroo Jesse","email":"111222333@ZZAMZ4WT4Y3E.monnify"}},"eventType":"SUCCESSFUL_TRANSACTION"}`)
	const secret = "91MUDL9N6U3BQRXBQ2PJ9M0PW4J22M1Y"
	h := http.Header{}
	h.Set("monnify-signature", "f04fb635e04d71648bd3cc7999003da6861483342c856d05ddfa9b2dafacb873b0de1d0f8f67405d0010b4348b721c49fa171d317972618debba6b638aedcd3c")
	if err := connector.VerifyWebhook(spec, secret, h, body); err != nil {
		t.Errorf("documented example: %v", err)
	}
	m := hmac.New(sha512.New, []byte(creds["secret_key"]))
	m.Write(body)
	h.Set("monnify-signature", hex.EncodeToString(m.Sum(nil)))
	if err := connector.VerifyWebhook(spec, creds["secret_key"], h, body); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "other", h, body) == nil {
		t.Error("wrong secret accepted")
	}
	if connector.VerifyWebhook(spec, creds["secret_key"], h, append(body, ' ')) == nil {
		t.Error("tampered body accepted")
	}
	if connector.VerifyWebhook(spec, creds["secret_key"], http.Header{}, body) == nil {
		t.Error("unsigned delivery accepted")
	}
}

func TestTriggerExpressions(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"]
	e := expr.MustNewWithRoots("body", "headers", "query")
	for _, c := range []struct{ body, event, dedup, correlation string }{
		{`{"eventType":"SUCCESSFUL_DISBURSEMENT","eventData":{"amount":10,"transactionReference":"MFDS|20210317032332|002431","fee":8,"reference":"tsk_1","status":"SUCCESS"}}`,
			"SUCCESSFUL_DISBURSEMENT", "SUCCESSFUL_DISBURSEMENT:MFDS|20210317032332|002431", "tsk_1"},
		{`{"eventType":"FAILED_DISBURSEMENT","eventData":{"transactionReference":"MFDS|2","reference":"tsk_2","status":"FAILED"}}`,
			"FAILED_DISBURSEMENT", "FAILED_DISBURSEMENT:MFDS|2", "tsk_2"},
		{`{"eventType":"REVERSED_DISBURSEMENT","eventData":{"transactionReference":"MFDS3","reference":"tsk_3","status":"REVERSED"}}`,
			"REVERSED_DISBURSEMENT", "REVERSED_DISBURSEMENT:MFDS3", "tsk_3"},
		{`{"eventType":"SUCCESSFUL_TRANSACTION","eventData":{"product":{"reference":"acct-ref-1","type":"RESERVED_ACCOUNT"},"transactionReference":"MNFY|04|1","paymentReference":"MNFY|04|1","amountPaid":3000}}`,
			"SUCCESSFUL_TRANSACTION", "SUCCESSFUL_TRANSACTION:MNFY|04|1", "acct-ref-1"},
		{`{"eventData":{"product":{"reference":"order-17","type":"WEB_SDK"},"transactionReference":"MNFY|23|2","paymentReference":"order-17"},"eventType":"SUCCESSFUL_TRANSACTION"}`,
			"SUCCESSFUL_TRANSACTION", "SUCCESSFUL_TRANSACTION:MNFY|23|2", "order-17"},
		{`{"eventData":{"transactionReference":"MNFY|85|3","paymentReference":"order-18","paymentRejectionInformation":{"rejectionReason":"UNDER_PAYMENT"}},"eventType":"REJECTED_PAYMENT"}`,
			"REJECTED_PAYMENT", "REJECTED_PAYMENT:MNFY|85|3", "order-18"},
		{`{"eventType":"SUCCESSFUL_REFUND","eventData":{"transactionReference":"MNFY|9","refundReference":"ref001","refundStatus":"COMPLETED"}}`,
			"SUCCESSFUL_REFUND", "SUCCESSFUL_REFUND:ref001", "ref001"},
		{`{"eventData":{"amount":"1199.00","settlementReference":"LB8HG1PNZT4ATJGZXQBY","transactionsCount":1},"eventType":"SETTLEMENT"}`,
			"SETTLEMENT", "SETTLEMENT:LB8HG1PNZT4ATJGZXQBY", "LB8HG1PNZT4ATJGZXQBY"},
		{`{"eventData":{"externalMandateReference":"mfy-mandate-102","mandateStatus":"CANCELLED","mandateCode":"MTDD|01J3"},"eventType":"MANDATE_UPDATE"}`,
			"MANDATE_UPDATE", "MANDATE_UPDATE:MTDD|01J3:CANCELLED", "mfy-mandate-102"},
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
