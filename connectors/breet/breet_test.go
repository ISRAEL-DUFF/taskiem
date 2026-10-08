package breet

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

const ref = "tsk_ab12cd34ef56gh78ij90kl12mn34op56"

func call(t *testing.T, action string, input map[string]any, creds map[string]string, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL})
	cr := map[string]string{"app_id": "app-1", "app_secret": "sec-1", "pin": "1234", "environment": "sandbox"}
	for k, v := range creds {
		cr[k] = v
	}
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), IdempotencyKey: ref, Attempt: 1, Credentials: cr})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

var cryptoIn = map[string]any{"amount_usd": 2500, "token": "USDT", "network": "SOL", "wallet_address": "14grJpemFaf88c8tiVb77W7TYg2W3ir6pfkKz3YjhhZ5", "external_id": ref}

func TestReads(t *testing.T) {
	r, err := call(t, "list_assets", nil, nil, "assets")
	if err != nil || out(r)["assets"].([]any)[0].(map[string]any)["minimum"] != "5" {
		t.Errorf("assets %v %v", r.Output, err)
	}
	r, err = call(t, "get_balances", nil, nil, "integration")
	if err != nil {
		t.Fatal(err)
	}
	b := out(r)["balances"].([]any)
	ngn := b[0].(map[string]any)
	if ngn["currency"] != "NGN" || ngn["balance"] != int64(2067286025) || ngn["balance_exact"] != "20672860.25790078" {
		t.Errorf("balances %v", b)
	}
	if raw, _ := json.Marshal(r.Output); strings.Contains(string(raw), "whsec") || strings.Contains(string(raw), "ab@example.com") {
		t.Error("the integration's secret or owner details leaked into the output")
	}
	r, err = call(t, "get_deposit", map[string]any{"trade_id": "692f91aa729255932afe9078"}, nil, "deposit")
	if err != nil || out(r)["amount_usd"] != "21.379427842514175" || out(r)["crypto_received"] != "0.00025406" || out(r)["currency"] != "NGN" {
		t.Errorf("deposit %v %v", r.Output, err)
	}
	r, err = call(t, "verify_bank_account", map[string]any{"bank_id": "39", "account_number": "3154021148", "currency": "ngn"}, nil, "bank_validate")
	if err != nil || out(r)["account_name"] != "CHIROMA AHMED OLABANJI" {
		t.Errorf("verify %v %v", r.Output, err)
	}
}

func TestAddressesAreOnePerLabel(t *testing.T) {
	in := map[string]any{"asset_id": "67063f653b4a1f6c7a60ec58", "label": "user-12345", "bank_id": "39", "account_number": "3154021148", "auto_settlement": true}
	r, err := call(t, "generate_address", in, nil, "address")
	if err != nil || out(r)["address"] != "TV8dNYYBgL3xLbQcJLMBNavY4gYNqPF8Jv" {
		t.Fatalf("%v %v", r.Output, err)
	}
	r, err = call(t, "generate_address", in, nil, "address_exists", "wallets")
	if err != nil || out(r)["wallet_id"] != "6932dc98c2ceccdea367388a" {
		t.Errorf("existing: %v %v", r.Output, err)
	}
	r, err = call(t, "add_bank", map[string]any{"bank_id": "39", "account_number": "3154021148", "currency": "ngn"}, nil, "bank_add_exists", "saved_banks")
	if err != nil || out(r)["saved_bank_id"] != "69737920df8b52679a8b198e" {
		t.Errorf("saved bank: %v %v", r.Output, err)
	}
}

func TestCryptoWithdrawal(t *testing.T) {
	r, err := call(t, "withdraw_crypto", cryptoIn, nil, "withdraw_crypto", "withdrawal_pending")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "pending" || o["amount"] != int64(2500) || o["fee"] != int64(183) || o["external_id"] != ref || o["crypto_amount"] != "23.175" {
		t.Errorf("output %v", o)
	}
	// A retry refused as a duplicate reports the withdrawal that got through.
	r, err = call(t, "withdraw_crypto", cryptoIn, nil, "withdraw_crypto_duplicate", "withdrawal_by_ref")
	if err != nil || out(r)["status"] != "processing" {
		t.Errorf("duplicate: %v %v", r.Output, err)
	}
	// Someone else's identical withdrawal within the minute: ours never happened.
	_, err = call(t, "withdraw_crypto", cryptoIn, nil, "withdraw_crypto_duplicate", "withdrawal_by_ref_missing")
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("duplicate, not ours: %v", err)
	}
	_, err = call(t, "withdraw_crypto", cryptoIn, nil, "withdraw_crypto_duplicate", "withdrawal_by_ref_rejected")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "address blocked") {
		t.Errorf("rejected: %v", err)
	}
	if _, err := call(t, "withdraw_crypto", cryptoIn, nil, "withdraw_crypto_insufficient"); effects.Classify(err) != effects.KindFatal {
		t.Errorf("insufficient: %v", err)
	}
	if _, err := call(t, "withdraw_crypto", cryptoIn, nil, "withdraw_crypto_server_error"); effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("500: %v", err)
	}
	if _, err := call(t, "withdraw_crypto", cryptoIn, map[string]string{"pin": ""}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no pin: %v", err)
	}
}

func TestReconcileByExternalID(t *testing.T) {
	r, err := call(t, "get_withdrawal", map[string]any{"external_id": ref}, nil, "withdrawal_by_ref")
	if err != nil || out(r)["withdrawal_id"] != "697251ed397a53d0cb7b228b" {
		t.Errorf("%v %v", r.Output, err)
	}
	_, err = call(t, "get_withdrawal", map[string]any{"external_id": ref}, nil, "withdrawal_by_ref_missing")
	if !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
}

func TestBankWithdrawalNeedsTheConfirmedUnit(t *testing.T) {
	in := map[string]any{"saved_bank_id": "69737920df8b52679a8b198e", "amount": 500050, "currency": "NGN", "narration": "Payout", "external_id": ref}
	// Unset: refused before anything is sent.
	_, err := call(t, "withdraw_to_bank", in, nil)
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "bank_withdrawal_unit") {
		t.Errorf("unset: %v", err)
	}
	if _, err := call(t, "withdraw_to_bank", in, map[string]string{"bank_withdrawal_unit": "usd"}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("NGN step on a USD connection: %v", err)
	}
	r, err := call(t, "withdraw_to_bank", in, map[string]string{"bank_withdrawal_unit": "local"}, "withdraw_bank", "withdrawal_pending")
	if err != nil || out(r)["withdrawal_id"] != "697251ed397a53d0cb7b228b" {
		t.Errorf("local: %v %v", r.Output, err)
	}
}

func TestWebhookSecret(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"].Verify
	h := http.Header{}
	h.Set("x-webhook-secret", "breet_whsec_1")
	if err := connector.VerifyWebhook(spec, "breet_whsec_1", h, []byte(`{}`)); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "breet_whsec_2", h, []byte(`{}`)) == nil {
		t.Error("wrong secret accepted")
	}
}
