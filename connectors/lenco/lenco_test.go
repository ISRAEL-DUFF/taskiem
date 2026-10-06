package lenco

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
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
		Credentials: map[string]string{"api_token": "lenco_test_token", "account_id": "056ffebf-812a-433a-83a0-9c67cd8c089c"}})
}

var transferIn = map[string]any{"amount": 200000, "account_number": "8144374977", "bank_code": "000014", "narration": "Sept salary", "reference": ref}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func TestRegistersBothHosts(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("lenco@1")
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "api.lenco.co" || h[1] != "sandbox.lenco.co" {
		t.Errorf("hosts %v", h)
	}
	cl := &client{live: "L", sandbox: "S"}
	if cl.base(connector.Request{Credentials: map[string]string{"environment": "Sandbox"}}) != "S" || cl.base(connector.Request{}) != "L" {
		t.Error("environment selection")
	}
}

func TestReadsConvertNairaToKobo(t *testing.T) {
	r, err := call(t, "list_accounts", nil, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	a := out(r)["accounts"].([]any)[0].(map[string]any)
	if a["available_balance"] != int64(9999540431607) || a["account_number"] != "0000000077" {
		t.Errorf("account %v", a)
	}
	r, err = call(t, "get_balance", nil, "balance")
	if err != nil || out(r)["available_balance"] != int64(125050) || out(r)["current_balance"] != int64(130000) {
		t.Errorf("balance %v %v", r.Output, err)
	}
	if r, err := call(t, "list_banks", nil, "list_banks"); err != nil || len(out(r)["banks"].([]any)) != 2 {
		t.Errorf("banks %v %v", r.Output, err)
	}
	r, err = call(t, "resolve_account", map[string]any{"account_number": "8144374977", "bank_code": "000014"}, "resolve")
	if err != nil || out(r)["account_name"] != "Shalewa Elizabeth" || out(r)["bank_name"] != "ACCESS BANK" {
		t.Errorf("resolve %v %v", r.Output, err)
	}
	_, err = call(t, "resolve_account", map[string]any{"account_number": "8144374977", "bank_code": "000014"}, "resolve_unverified")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "could not be verified") {
		t.Errorf("unverified: %v", err)
	}
}

func TestTransferSendsNairaAndReturnsPending(t *testing.T) {
	r, err := call(t, "transfer", transferIn, "transfer_queued")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "pending" || o["reference"] != ref || o["amount"] != int64(200000) || o["transaction_id"] != "" {
		t.Errorf("output %v", o)
	}
}

func TestRepeatReportsTheOriginalTransfer(t *testing.T) {
	// A resend after a lost response: Lenco refuses the reference, and the
	// connector reports the transfer already made under it.
	r, err := call(t, "transfer", transferIn, "transfer_duplicate", "by_reference_successful")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "successful" || o["fee"] != int64(1075) || o["nip_session_id"] == "" || o["amount"] != int64(200000) {
		t.Errorf("output %v", o)
	}
	// The original failed: no money moved; the step fails with the reason.
	_, err = call(t, "transfer", transferIn, "transfer_duplicate", "by_reference_failed")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Insufficient funds") {
		t.Errorf("failed original: %v", err)
	}
	// Refused, yet nothing under the reference: a bad reference, not a payment.
	_, err = call(t, "transfer", transferIn, "transfer_duplicate", "by_reference_not_found")
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("refused and absent: %v", err)
	}
	// "declined" is undocumented: a person decides.
	_, err = call(t, "transfer", transferIn, "transfer_duplicate", "by_reference_declined")
	if effects.Classify(err) != effects.KindIndeterminate {
		t.Errorf("declined: %v", err)
	}
}

func TestTransferErrors(t *testing.T) {
	_, err := call(t, "transfer", transferIn, "transfer_server_error")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("502: %v", err)
	}
	_, err = call(t, "transfer", transferIn, "transfer_insufficient")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "code 02") {
		t.Errorf("insufficient: %v", err)
	}
	// Fractional kobo and a missing destination never reach Lenco.
	bad := map[string]any{"amount": 100.5, "account_number": "1", "bank_code": "1", "narration": "x", "reference": ref}
	if _, err := call(t, "transfer", bad); effects.Classify(err) != effects.KindFatal {
		t.Errorf("fractional kobo: %v", err)
	}
	if _, err := call(t, "transfer", map[string]any{"amount": 100, "narration": "x", "reference": ref}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("no destination: %v", err)
	}
}

func TestGetTransfer(t *testing.T) {
	r, err := call(t, "get_transfer", map[string]any{"reference": ref}, "by_reference_failed")
	if err != nil || out(r)["status"] != "failed" || out(r)["reason_for_failure"] != "Insufficient funds in your account" {
		t.Errorf("failed read: %v %v", r.Output, err)
	}
	_, err = call(t, "get_transfer", map[string]any{"reference": ref}, "by_reference_not_found")
	if !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
}

func TestVirtualAccountAmountsAreNaira(t *testing.T) {
	r, err := call(t, "create_virtual_account", map[string]any{"account_name": "Acme / Order 1", "transaction_reference": "order-1", "amount": 150050}, "virtual_account")
	if err != nil || out(r)["account_number"] != "9999000086" || out(r)["account_reference"] != "f0f1bcb4-ff82-4332-9521-2e5973089dfc" {
		t.Errorf("%v %v", r.Output, err)
	}
	if _, err := call(t, "create_virtual_account", map[string]any{"account_name": "x", "is_static": true}); effects.Classify(err) != effects.KindFatal {
		t.Errorf("static without BVN: %v", err)
	}
}

func TestWebhookSignature(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["event"].Verify
	body := []byte(`{"event":"transaction.successful","data":{"id":"46ce","clientReference":"` + ref + `"}}`)
	key := sha256.Sum256([]byte("lenco_test_token"))
	m := hmac.New(sha512.New, []byte(hex.EncodeToString(key[:])))
	m.Write(body)
	h := http.Header{}
	h.Set("X-Lenco-Signature", hex.EncodeToString(m.Sum(nil)))
	if err := connector.VerifyWebhook(spec, "lenco_test_token", h, body); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "other_token", h, body) == nil {
		t.Error("wrong token accepted")
	}
	if connector.VerifyWebhook(spec, "lenco_test_token", h, append(body, ' ')) == nil {
		t.Error("tampered body accepted")
	}
}
