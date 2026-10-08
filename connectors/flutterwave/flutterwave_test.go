package flutterwave

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

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
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), IdempotencyKey: ref, Attempt: 1,
		Credentials: map[string]string{"secret_key": "FLWSECK_TEST-abc"}})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

var payIn = map[string]any{"amount": 550000, "account_number": "0690000040", "bank_code": "044", "narration": "Sept salary", "reference": ref}

func TestReadsInMinorUnits(t *testing.T) {
	r, err := call(t, "get_balance", nil, "balance")
	if err != nil || out(r)["available_balance"] != int64(236784000) || out(r)["ledger_balance"] != int64(25312582) {
		t.Errorf("balance %v %v", r.Output, err)
	}
	if r, err := call(t, "list_banks", nil, "banks"); err != nil || len(out(r)["banks"].([]any)) != 1 {
		t.Errorf("banks %v %v", r.Output, err)
	}
	r, err = call(t, "resolve_account", map[string]any{"account_number": "0690000032", "bank_code": "044"}, "resolve")
	if err != nil || out(r)["account_name"] != "Pastor Bright" {
		t.Errorf("resolve %v %v", r.Output, err)
	}
	r, err = call(t, "get_transfer_fee", map[string]any{"amount": 550000}, "fee")
	if err != nil || out(r)["fee"] != int64(2688) { // 26.875 naira: fees round up to the kobo
		t.Errorf("fee %v %v", r.Output, err)
	}
}

func TestPayoutSendsWholeNaira(t *testing.T) {
	r, err := call(t, "transfer", payIn, "transfer_new")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "NEW" || o["amount"] != int64(550000) || o["fee"] != int64(2688) || o["transfer_id"] != int64(26251) || o["requires_approval"] != false {
		t.Errorf("output %v", o)
	}
	r, err = call(t, "transfer", payIn, "transfer_awaiting_approval")
	if err != nil || out(r)["requires_approval"] != true {
		t.Errorf("approval: %v %v", r.Output, err)
	}
	for _, kobo := range []any{550050, 99, 5500.5} {
		in := map[string]any{"amount": kobo, "account_number": "1", "bank_code": "044", "reference": ref}
		if _, err := call(t, "transfer", in); effects.Classify(err) != effects.KindFatal {
			t.Errorf("%v kobo: %v", kobo, err)
		}
	}
}

func TestRepeatReportsTheOriginal(t *testing.T) {
	r, err := call(t, "transfer", payIn, "transfer_duplicate", "by_reference_successful")
	if err != nil || out(r)["status"] != "SUCCESSFUL" {
		t.Fatalf("%v %v", r.Output, err)
	}
	_, err = call(t, "transfer", payIn, "transfer_duplicate", "by_reference_failed")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "DISBURSE FAILED") {
		t.Errorf("failed original: %v", err)
	}
	// Refused yet not listed (yet): never assume it did not happen.
	_, err = call(t, "transfer", payIn, "transfer_duplicate", "by_reference_none")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("refused, not listed: %v", err)
	}
}

func TestTimeoutIsAnUnknownOutcome(t *testing.T) {
	_, err := call(t, "transfer", payIn, "transfer_timeout")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("503: %v", err)
	}
	_, err = call(t, "transfer", payIn, "transfer_invalid")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Invalid bank code") {
		t.Errorf("400: %v", err)
	}
}

func TestLookups(t *testing.T) {
	_, err := call(t, "get_transfer", map[string]any{"transfer_id": 999}, "get_transfer_not_found")
	if !errors.Is(err, connector.ErrNotFound) || effects.Classify(err) != effects.KindFatal {
		t.Errorf("not found: %v", err)
	}
	_, err = call(t, "get_transfer_by_reference", map[string]any{"reference": ref}, "by_reference_none")
	if !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("by reference: %v", err)
	}
	r, err := call(t, "verify_payment", map[string]any{"tx_ref": "order-17"}, "verify_by_ref")
	if err != nil || out(r)["amount"] != int64(150050) || out(r)["status"] != "successful" {
		t.Errorf("verify %v %v", r.Output, err)
	}
}

func TestWebhookSecretHash(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"].Verify
	h := http.Header{}
	h.Set("verif-hash", "my-hash")
	if err := connector.VerifyWebhook(spec, "my-hash", h, []byte(`{}`)); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "other", h, []byte(`{}`)) == nil {
		t.Error("wrong hash accepted")
	}
}
