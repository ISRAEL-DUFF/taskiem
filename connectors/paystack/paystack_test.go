package paystack

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

const ref = "tsk_ab12cd34ef56gh78ij90kl12mn34op56"

func call(t *testing.T, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{
		Input: input, Credentials: map[string]string{"secret_key": "sk_test_xyz"}, HTTP: srv.Client(),
	})
}

func TestManifestHandlersMatch(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	if c, _ := reg.Get("paystack@1"); c.Manifest.Hosts()[0] != "api.paystack.co" {
		t.Errorf("hosts %v", c.Manifest.Hosts())
	}
}

func TestCheckBalance(t *testing.T) {
	r, err := call(t, "check_balance", nil, "balance")
	if err != nil {
		t.Fatal(err)
	}
	b := r.Output.(map[string]any)["balances"].([]any)[0].(map[string]any)
	if b["currency"] != "NGN" || b["balance"] != float64(5000000) {
		t.Errorf("balance %v", b)
	}
}

func TestTransfer(t *testing.T) {
	r, err := call(t, "transfer", map[string]any{"amount": 100000, "recipient": "RCP_t6wnmj2a9mp4m8k", "reason": "Salary Oct 2026", "reference": ref}, "transfer_queued")
	if err != nil {
		t.Fatal(err)
	}
	out := r.Output.(map[string]any)
	if out["status"] != "pending" || out["transfer_code"] != "TRF_v5tip3zx8nna9o78" || out["reference"] != ref {
		t.Errorf("output %v", out)
	}
}

func TestDuplicateReferenceReturnsExistingTransfer(t *testing.T) {
	r, err := call(t, "transfer", map[string]any{"amount": 100000, "recipient": "RCP_t6wnmj2a9mp4m8k", "reference": ref}, "transfer_duplicate", "verify_transfer_success")
	if err != nil {
		t.Fatal(err)
	}
	if r.Output.(map[string]any)["status"] != "success" {
		t.Errorf("output %v", r.Output)
	}
}

func TestErrorClassification(t *testing.T) {
	_, err := call(t, "transfer", map[string]any{"amount": 1, "recipient": "R", "reference": ref}, "transfer_server_error")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("502 may follow a transfer that happened: want unknown outcome, got %v", err)
	}
	_, err = call(t, "transfer", map[string]any{"amount": 1, "recipient": "R", "reference": ref}, "transfer_insufficient")
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("insufficient balance should be fatal: %v", err)
	}
	_, err = call(t, "transfer", map[string]any{"amount": 1, "recipient": "R"})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("missing reference should be fatal: %v", err)
	}
}

func TestVerifyTransferNotFoundForReconcile(t *testing.T) {
	_, err := call(t, "verify_transfer", map[string]any{"reference": ref}, "verify_transfer_not_found")
	if !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestVerifyCharge(t *testing.T) {
	r, err := call(t, "verify_charge", map[string]any{"reference": "T685312322670591"}, "verify_charge")
	if err != nil || r.Output.(map[string]any)["amount"] != float64(20000) {
		t.Errorf("%v %v", r.Output, err)
	}
}

func TestWebhookSignature(t *testing.T) {
	m := New(Options{}).Manifest
	spec := m.Triggers["transfer_event"].Verify
	body := []byte(`{"event":"transfer.success","data":{"reference":"` + ref + `"}}`)
	mac := hmac.New(sha512.New, []byte("sk_test_xyz"))
	mac.Write(body)
	h := http.Header{}
	h.Set("x-paystack-signature", hex.EncodeToString(mac.Sum(nil)))
	if err := connector.VerifyWebhook(spec, "sk_test_xyz", h, body); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}
	if err := connector.VerifyWebhook(spec, "sk_test_other", h, body); !errors.Is(err, connector.ErrBadSignature) {
		t.Error("signature under the wrong secret accepted")
	}
	if err := connector.VerifyWebhook(spec, "sk_test_xyz", h, append(body, ' ')); !errors.Is(err, connector.ErrBadSignature) {
		t.Error("tampered body accepted")
	}
}

func TestInitializeTransaction(t *testing.T) {
	r, err := call(t, "initialize_transaction", map[string]any{"reference": ref, "amount": 300000, "email": "bola@example.com",
		"callback_url": "https://ada-kitchen.shops.test/o/AK-7QX4KD"}, "initialize_transaction")
	out, _ := r.Output.(map[string]any)
	if err != nil || out["authorization_url"] != "https://checkout.paystack.com/0peioxfhpn" || out["reference"] != ref {
		t.Fatalf("%v %v", out, err)
	}
	if _, err := call(t, "initialize_transaction", map[string]any{"amount": 300000, "email": "bola@example.com"}); err == nil {
		t.Fatal("a payment page without a reference")
	}
}

// A refund is made once: Paystack refusing a second refund of the same
// payment is reported as the first; its other refusals are final.
func TestRefund(t *testing.T) {
	r, err := call(t, "refund_transaction", map[string]any{"reference": ref, "customer_note": "Order declined"}, "refund_created")
	out, _ := r.Output.(map[string]any)
	if err != nil || out["status"] != "pending" || out["reference"] != ref || out["refund_id"] == nil {
		t.Fatalf("created: %v %v", out, err)
	}
	r, err = call(t, "refund_transaction", map[string]any{"reference": ref}, "refund_already")
	if out, _ := r.Output.(map[string]any); err != nil || out["status"] != "already" {
		t.Fatalf("again: %v %v", out, err)
	}
	if _, err := call(t, "refund_transaction", map[string]any{"reference": ref}, "refund_insufficient"); !errors.Is(err, effects.ErrFatal) ||
		!strings.Contains(err.Error(), "Insufficient balance") {
		t.Fatalf("no balance: %v", err)
	}
	if _, err := call(t, "refund_transaction", map[string]any{}); !errors.Is(err, effects.ErrFatal) {
		t.Fatalf("no reference: %v", err)
	}
	if r, err := call(t, "refund_status", map[string]any{"reference": ref}, "refund_status_reversed"); err != nil || r.Output.(map[string]any)["status"] != "reversed" {
		t.Fatalf("reversed: %v %v", r.Output, err)
	}
	if _, err := call(t, "refund_status", map[string]any{"reference": ref}, "refund_status_paid"); !errors.Is(err, connector.ErrNotFound) {
		t.Fatalf("not refunded: %v", err)
	}
}

// Paying people by bank and account number (no-technical-steps N8): the
// banks by name, the account's name to confirm, and a recipient made from
// the bank's name as a sheet has it.
func TestRecipientsFromBankAndAccount(t *testing.T) {
	r, err := call(t, "list_banks", nil, "list_banks")
	if err != nil || len(r.Output.(map[string]any)["banks"].([]any)) != 8 {
		t.Fatalf("banks: %v %v", r.Output, err)
	}
	r, err = call(t, "resolve_account", map[string]any{"account_number": "0123456789", "bank_code": "058"}, "resolve_account")
	if err != nil || r.Output.(map[string]any)["account_name"] != "BOLA ADE" {
		t.Fatalf("resolve: %v %v", r.Output, err)
	}
	if _, err := call(t, "resolve_account", map[string]any{"account_number": "12345", "bank_code": "058"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("a short account number: %v", err)
	}
	r, err = call(t, "create_recipient", map[string]any{"account_number": "0123456789", "bank": "GTBank", "name": "Bola Ade"}, "list_banks", "create_recipient")
	if err != nil || r.Output.(map[string]any)["recipient_code"] != "RCP_2x5j67tnnw1t98k" {
		t.Fatalf("recipient: %v %v", r.Output, err)
	}
	if _, err := call(t, "create_recipient", map[string]any{"account_number": "0123456789", "bank": "Bank of Nowhere", "name": "Bola Ade"}, "list_banks"); err == nil || !strings.Contains(err.Error(), "no single bank") {
		t.Errorf("an unknown bank: %v", err)
	}
}

func TestBankKey(t *testing.T) {
	for in, want := range map[string]string{"GTBank": "guaranty trust", "Guaranty Trust Bank": "guaranty trust", "gtb": "guaranty trust", "First Bank": "first",
		"First Bank of Nigeria": "first", "UBA": "united for africa", "United Bank For Africa": "united for africa", "Zenith Bank PLC": "zenith",
		"Access": "access", "Access Bank (Diamond)": "access diamond", "OPay": "opay"} {
		if got := BankKey(in); got != want {
			t.Errorf("BankKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCapabilities(t *testing.T) {
	fixture.Capabilities(t, func(base string) *connector.Connector { return New(Options{BaseURL: base}) })
}

func TestPayWithTransferAsksForTheExpiry(t *testing.T) {
	now := time.Date(2026, 10, 23, 18, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		minutes any
		want    string
	}{
		{30, "2026-10-23T18:30:00Z"},
		{5, "2026-10-23T18:15:00Z"},   // under 15 minutes: Paystack's minimum
		{900, "2026-10-24T02:00:00Z"}, // over 8 hours: its maximum
		{nil, "2026-10-24T02:00:00Z"}, // none: 8 hours, as Paystack does
	} {
		var got map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = w.Write([]byte(`{"status":true,"data":{"status":"pending_bank_transfer","reference":"` + ref + `","amount":2500000,"account_number":"1231084927","account_name":"PAYSTACK CHECKOUT","bank":{"name":"Test Bank"},"account_expires_at":"x"}}`))
		}))
		in := map[string]any{"amount": 2500000, "email": "ada@example.com", "reference": ref}
		if c.minutes != nil {
			in["expires_minutes"] = c.minutes
		}
		conn := New(Options{BaseURL: srv.URL, Now: func() time.Time { return now }})
		r, err := conn.Actions["pay_with_transfer"].Execute(context.Background(), connector.Request{Input: in, Credentials: map[string]string{"secret_key": "sk_test_xyz"}, HTTP: srv.Client()})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		bt, _ := got["bank_transfer"].(map[string]any)
		if bt["account_expires_at"] != c.want || got["reference"] != ref || got["email"] != "ada@example.com" {
			t.Errorf("minutes %v: sent %v", c.minutes, got)
		}
		if out := r.Output.(map[string]any); out["account_number"] != "1231084927" || out["bank_name"] != "Test Bank" {
			t.Errorf("output %v", out)
		}
	}
}

func TestPayWithTransferWithoutAnAccountIsIndeterminate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":true,"data":{"status":"failed","reference":"` + ref + `"}}`))
	}))
	defer srv.Close()
	_, err := New(Options{BaseURL: srv.URL}).Actions["pay_with_transfer"].Execute(context.Background(), connector.Request{
		Input: map[string]any{"amount": 100, "email": "a@b.co", "reference": ref}, Credentials: map[string]string{"secret_key": "k"}, HTTP: srv.Client()})
	if !errors.Is(err, effects.ErrIndeterminate) {
		t.Errorf("err %v", err)
	}
}
