package moniepointpos

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
)

const ref = "tsk_ab12cd34ef56gh78ij90kl12mn34op56"

var creds = map[string]string{"client_id": "api-client-taskiem", "client_secret": "pos-secret-0000000000000001", "webhook_secret": "whsec-0001"}

var pushIn = map[string]any{"terminal_serial": "P260TEST01", "amount": 1100000, "payment_method": "CARD_PURCHASE", "merchant_reference": ref}

// call runs one action against a fresh connector (so a fresh sign-in).
func call(t *testing.T, action string, input map[string]any, exchanges ...string) (connector.Response, error) {
	t.Helper()
	var exs []fixture.Exchange
	for _, n := range exchanges {
		exs = append(exs, fixture.Load(t, n))
	}
	srv := fixture.Serve(t, exs...)
	c := New(Options{BaseURL: srv.URL})
	return c.Actions[action].Execute(context.Background(), connector.Request{Input: input, HTTP: srv.Client(), IdempotencyKey: ref, Attempt: 1, Credentials: creds})
}

func out(r connector.Response) map[string]any { return r.Output.(map[string]any) }

func TestRegistersBothHosts(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("moniepoint_pos@1")
	if !ok {
		t.Fatal("moniepoint_pos@1 not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "channel.moniepoint.com" || h[1] != "moniepoint-pos-backend-service.development.moniepoint.com" {
		t.Errorf("hosts %v", h)
	}
	if c.Verifiers["transaction"] == nil {
		t.Error("the transaction trigger has no verifier")
	}
	cl := &client{live: "L", sandbox: "S"}
	if cl.base(connector.Request{Credentials: map[string]string{"environment": " Sandbox"}}) != "S" || cl.base(connector.Request{}) != "L" {
		t.Error("environment selection")
	}
}

func TestTokenIsCachedRenewedAndRefreshedOn401(t *testing.T) {
	srv := fixture.Serve(t,
		fixture.Load(t, "auth"), fixture.Load(t, "status_processed"), fixture.Load(t, "status_processed"), // one sign-in serves two calls
		fixture.Load(t, "auth2"), fixture.Load(t, "status_processed_tok2"), // expired: a new sign-in
	)
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	c := newConnector(Options{BaseURL: srv.URL}, func() time.Time { return now })
	get := c.Actions["get_transaction"]
	req := connector.Request{Input: map[string]any{"merchant_reference": ref}, HTTP: srv.Client(), Credentials: creds, Attempt: 1}
	for i := 0; i < 2; i++ {
		r, err := get.Execute(context.Background(), req)
		if err != nil || out(r)["processing_status"] != "PROCESSED" {
			t.Fatalf("call %d: %v %v", i, r.Output, err)
		}
	}
	now = now.Add(8699*time.Second - 30*time.Second) // past expiresIn less the minute's margin
	if _, err := get.Execute(context.Background(), req); err != nil {
		t.Fatalf("after expiry: %v", err)
	}

	// Moniepoint revokes a token early: one 401, a new sign-in, the call again.
	srv2 := fixture.Serve(t, fixture.Load(t, "auth"), fixture.Load(t, "status_expired_token"), fixture.Load(t, "auth2"), fixture.Load(t, "status_processed_tok2"))
	c2 := New(Options{BaseURL: srv2.URL})
	if _, err := c2.Actions["get_transaction"].Execute(context.Background(), connector.Request{Input: map[string]any{"merchant_reference": ref}, HTTP: srv2.Client(), Credentials: creds, Attempt: 1}); err != nil {
		t.Errorf("401 refresh: %v", err)
	}
}

func TestCheckAccess(t *testing.T) {
	r, err := call(t, "check_access", nil, "auth")
	if err != nil || out(r)["ok"] != true || out(r)["expires_in"] != int64(8699) {
		t.Errorf("check_access %v %v", r.Output, err)
	}
	_, err = call(t, "check_access", nil, "auth_refused")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Invalid credentials") {
		t.Errorf("refused sign-in: %v", err)
	}
	_, err = call(t, "check_access", nil, "auth_down")
	if effects.Classify(err) != effects.KindRetryable {
		t.Errorf("sign-in with the provider down: %v", err)
	}
	c := New(Options{BaseURL: "https://example.test"})
	_, err = c.Actions["check_access"].Execute(context.Background(), connector.Request{Credentials: map[string]string{"client_id": "x"}})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("no secret: %v", err)
	}
}

func TestRequestPayment(t *testing.T) {
	r, err := call(t, "request_payment", pushIn, "auth", "push_accepted", "status_pending")
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	if o["processing_status"] != "PENDING" || o["merchant_reference"] != ref || o["request_amount"] != int64(1100000) || o["terminal_serial"] != "P260TEST01" {
		t.Errorf("accepted: %v", o)
	}
	if _, ok := o["actual_amount"]; ok {
		t.Errorf("actual_amount should be absent until the terminal answers: %v", o)
	}

	// Accepted, but the status could not be read: the push still succeeded.
	r, err = call(t, "request_payment", pushIn, "auth", "push_accepted", "status_missing")
	if err != nil || out(r)["processing_status"] != "PENDING" || out(r)["requested_payment_method"] != "CARD_PURCHASE" {
		t.Errorf("accepted without a readable status: %v %v", r.Output, err)
	}

	// A resend after a lost answer: refused as existing, reported from the record.
	r, err = call(t, "request_payment", pushIn, "auth", "push_exists", "status_processed")
	if err != nil || out(r)["processing_status"] != "PROCESSED" || out(r)["actual_amount"] != int64(1100000) || out(r)["response_code"] != "00" {
		t.Errorf("existing: %v %v", r.Output, err)
	}
	// Refused as existing, yet nothing under the reference: unknown.
	_, err = call(t, "request_payment", pushIn, "auth", "push_exists", "status_missing")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("existing but missing: %v", err)
	}
	// Any other refusal: the terminal was not asked.
	_, err = call(t, "request_payment", pushIn, "auth", "push_bad_terminal")
	if effects.Classify(err) != effects.KindFatal || !strings.Contains(err.Error(), "Terminal not found") {
		t.Errorf("bad terminal: %v", err)
	}
	// A 500 may have reached the terminal: unknown, so the engine resends
	// under the same reference (and lands on the duplicate path).
	_, err = call(t, "request_payment", pushIn, "auth", "push_server_error")
	if effects.Classify(err) != effects.KindUnknownOutcome {
		t.Errorf("server error: %v", err)
	}

	for name, in := range map[string]map[string]any{
		"no terminal": {"amount": 100, "merchant_reference": ref},
		"half a kobo": {"terminal_serial": "P1", "amount": 100.5, "merchant_reference": ref},
		"zero":        {"terminal_serial": "P1", "amount": 0, "merchant_reference": ref},
		"bad method":  {"terminal_serial": "P1", "amount": 100, "payment_method": "CASH", "merchant_reference": ref},
		"no key":      {"terminal_serial": "P1", "amount": 100},
	} {
		c := New(Options{BaseURL: "https://example.test"})
		_, err := c.Actions["request_payment"].Execute(context.Background(), connector.Request{Input: in, Credentials: creds})
		if effects.Classify(err) != effects.KindFatal {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestGetTransaction(t *testing.T) {
	r, err := call(t, "get_transaction", map[string]any{"merchant_reference": ref}, "auth", "status_processed")
	if err != nil {
		t.Fatal(err)
	}
	o := out(r)
	if o["transaction_reference"] != "ATP|2MPT0073|1838496858930533333120" || o["actual_payment_method"] != "CARD_PURCHASE" || o["business_owner_id"] != int64(1) || o["response_message"] != "APPROVED" {
		t.Errorf("processed: %v", o)
	}
	_, err = call(t, "get_transaction", map[string]any{"merchant_reference": ref}, "auth", "status_missing")
	if effects.Classify(err) != effects.KindFatal || !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	_, err = call(t, "get_transaction", map[string]any{})
	if effects.Classify(err) != effects.KindFatal {
		t.Errorf("no reference: %v", err)
	}
}

// docEvent is the example payload from Moniepoint's webhook documentation.
const docEvent = `{
  "eventId": "7a35b4d4-f985-4c14-846a-173af3fa677c",
  "eventType": "V1_POS_TRANSFER_TRANSACTION",
  "subject": {"domain": "CHANNELS", "resource": "business", "resourceId": "00000"},
  "data": {
    "businessId": 0, "businessOwnerId": 0, "terminalSerial": "TEST", "amount": 10000,
    "transactionReference": "TEST", "merchantReference": null, "retrievalReferenceNumber": "",
    "transactionTime": "2026/08/04 09:49:25", "transactionType": "POS_TRANSFER", "transactionStatus": "COMPLETED",
    "responseCode": "20000", "responseMessage": "APPROVED", "customFields": {},
    "metaData": "{\"customerPhone\":null,\"narration\":\"TRANSF\",\"customerAccountNumber\":\"******4058\",\"customerBank\":\"Moniepoint MFB\",\"customerName\":\"TEST\"}",
    "narration": "TEST"
  },
  "createdAt": "2026-08-04T09:55:55.210365017"
}`

func TestDeliveriesAreVerified(t *testing.T) {
	const secret, id, ts = "whsec-0001", "b15ec58f-fa1f-4abb-8329-efaef8aa2bef", "1728651860073"
	body := []byte(docEvent)
	sig := Signature(secret, id, ts, body)
	// The documented recipe, computed independently of Signature.
	if want := "HvzIH3TaI0jFiMPbcuH4NblQ9Mmz+WKzodD1dpFlMHM="; Signature("your_secret_key", "your_webhook_id", "timestamp_value", []byte(`{"key": "value"}`)) != want {
		t.Errorf("the documentation's worked example does not verify")
	}
	h := func(id, ts, sig string) http.Header {
		out := http.Header{}
		if id != "" {
			out.Set(headerID, id)
		}
		if ts != "" {
			out.Set(headerTimestamp, ts)
		}
		if sig != "" {
			out.Set(headerSignature, sig)
		}
		return out
	}
	v := New(Options{}).Verifiers["transaction"]
	if err := v(secret, h(id, ts, sig), body); err != nil {
		t.Fatalf("a genuine delivery: %v", err)
	}
	tampered := []byte(strings.Replace(docEvent, `"amount": 10000`, `"amount": 1000000`, 1))
	for name, c := range map[string]struct {
		secret string
		h      http.Header
		body   []byte
	}{
		"tampered body":    {secret, h(id, ts, sig), tampered},
		"other timestamp":  {secret, h(id, "1728651860074", sig), body},
		"other id":         {secret, h("other", ts, sig), body},
		"wrong secret":     {"whsec-0002", h(id, ts, sig), body},
		"no secret":        {"", h(id, ts, sig), body},
		"no signature":     {secret, h(id, ts, ""), body},
		"no id":            {secret, h("", ts, sig), body},
		"no timestamp":     {secret, h(id, "", sig), body},
		"not base64":       {secret, h(id, ts, "!!"), body},
		"truncated base64": {secret, h(id, ts, sig[:20]), body},
	} {
		if err := v(c.secret, c.h, c.body); !errors.Is(err, connector.ErrBadSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
