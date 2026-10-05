package paystack

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"net/http"
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
	if effects.Classify(err) != effects.KindRetryable {
		t.Errorf("502 should be retryable: %v", err)
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
