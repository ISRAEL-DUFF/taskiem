package anchor

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // Anchor signs webhooks with HMAC-SHA1
	"encoding/base64"
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

func call(t *testing.T, action string, input map[string]any, attempt int, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), IdempotencyKey: ref, Attempt: attempt,
		Credentials: map[string]string{"api_key": "anc_test_key", "account_id": "166012843397415-anc_acc"}})
}

var payIn = map[string]any{"amount": 4500000, "account_number": "0690000031", "bank_code": "000014", "account_name": "Ada Obi", "reason": "Sept salary"}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func TestReads(t *testing.T) {
	r, err := call(t, "get_balance", nil, 1, "balance")
	if err != nil || out(r)["available_balance"] != int64(4950000) || out(r)["hold"] != int64(50000) {
		t.Errorf("balance %v %v", r.Output, err)
	}
	r, err = call(t, "list_banks", nil, 1, "banks")
	if err != nil || out(r)["banks"].([]any)[0].(map[string]any)["nip_code"] != "000014" {
		t.Errorf("banks %v %v", r.Output, err)
	}
	r, err = call(t, "verify_account", map[string]any{"bank_code": "000014", "account_number": "0000000010"}, 1, "verify_account")
	if err != nil || out(r)["account_name"] != "Test Account" {
		t.Errorf("verify %v %v", r.Output, err)
	}
}

func TestTransferChecksTheNameAndSendsTheKeyTwice(t *testing.T) {
	r, err := call(t, "transfer", payIn, 1, "counterparty", "transfer_pending")
	if err != nil {
		t.Fatal(err)
	}
	if o := out(r); o["status"] != "PENDING" || o["transfer_id"] != "16942554375340-anc_trsf" || o["reference"] != ref || o["amount"] != int64(4500000) {
		t.Errorf("output %v", o)
	}
	// The bank says the account is someone else's: nothing is sent.
	_, err = call(t, "transfer", payIn, 1, "counterparty_other_name")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "EMEKA OKAFOR") {
		t.Errorf("name mismatch: %v", err)
	}
	// Unless the step says not to check.
	unchecked := map[string]any{"check_name": false}
	for k, v := range payIn {
		unchecked[k] = v
	}
	if _, err := call(t, "transfer", unchecked, 1, "counterparty_other_name", "transfer_pending"); err != nil {
		t.Errorf("check_name false: %v", err)
	}
}

func TestResendAsksFirst(t *testing.T) {
	// The first attempt reached Anchor: the resend reports it, sends nothing.
	r, err := call(t, "transfer", payIn, 2, "counterparty", "by_reference_completed")
	if err != nil || out(r)["status"] != "COMPLETED" || out(r)["session_id"] == "" {
		t.Fatalf("resend after success: %v %v", r.Output, err)
	}
	// It did not: the resend sends.
	if _, err := call(t, "transfer", payIn, 2, "counterparty", "by_reference_not_found", "transfer_pending"); err != nil {
		t.Errorf("resend after nothing: %v", err)
	}
}

func TestTransferOutcomes(t *testing.T) {
	_, err := call(t, "transfer", payIn, 1, "counterparty", "transfer_failed")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "INSUFFICIENT_BALANCE") {
		t.Errorf("failed: %v", err)
	}
	_, err = call(t, "transfer", payIn, 1, "counterparty", "transfer_server_error")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("500: %v", err)
	}
	_, err = call(t, "transfer", payIn, 1, "counterparty", "transfer_invalid")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "reason must not be blank") {
		t.Errorf("400: %v", err)
	}
	r, err := call(t, "transfer", payIn, 1, "counterparty", "transfer_conflict", "by_reference_completed")
	if err != nil || out(r)["status"] != "COMPLETED" {
		t.Errorf("409 then found: %v %v", r.Output, err)
	}
	_, err = call(t, "transfer", payIn, 1, "counterparty", "transfer_conflict", "by_reference_not_found")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("409 not found: %v", err)
	}
	if _, err := call(t, "transfer", map[string]any{"amount": 99, "counterparty_id": "x", "reason": "r"}, 1); effects.Classify(err) != effects.KindFatal {
		t.Errorf("below NIP minimum: %v", err)
	}
}

func TestBookTransferAndReads(t *testing.T) {
	r, err := call(t, "book_transfer", map[string]any{"amount": 100000, "destination_account_id": "17000000000000-anc_acc", "reason": "Float top-up"}, 1, "book_transfer")
	if err != nil || out(r)["status"] != "COMPLETED" || out(r)["type"] != "BOOK_TRANSFER" {
		t.Errorf("book %v %v", r.Output, err)
	}
	r, err = call(t, "get_transfer", map[string]any{"transfer_id": "16942554375340-anc_trsf"}, 1, "verify_transfer_reversed")
	if err != nil || out(r)["status"] != "REVERSED" {
		t.Errorf("reversed read: %v %v", r.Output, err)
	}
	_, err = call(t, "get_transfer_by_reference", map[string]any{"reference": ref}, 1, "by_reference_not_found")
	if !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("by reference: %v", err)
	}
}

func TestSameName(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Ada Obi", "OBI ADA NGOZI", true},
		{"Ada N. Obi", "OBI ADA NGOZI", true},
		{"ada obi", "Ada-Obi", true},
		{"Ada Obi", "Emeka Okafor", false},
		{"Ada", "Ada Obi", false}, // one word cannot vouch for two
		{"Acme", "ACME", true},
		{"", "Ada", false},
		{"Ada Ada", "Ada Obi", false},
	} {
		if got := SameName(c.a, c.b); got != c.want {
			t.Errorf("SameName(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestWebhookSignature(t *testing.T) {
	spec := New(Options{}).Manifest.Triggers["transfer_event"].Verify
	body := []byte(`{"data":{"id":"evt-anc_et","type":"nip.transfer.successful","relationships":{"transfer":{"data":{"id":"16942554375340-anc_trsf"}}}}}`)
	m := hmac.New(sha1.New, []byte("whtoken"))
	m.Write(body)
	h := http.Header{}
	h.Set("x-anchor-signature", base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(m.Sum(nil)))))
	if err := connector.VerifyWebhook(spec, "whtoken", h, body); err != nil {
		t.Errorf("valid: %v", err)
	}
	if connector.VerifyWebhook(spec, "other", h, body) == nil {
		t.Error("wrong token accepted")
	}
}
