package mpesa

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/mpesa/internal/fakedaraja"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

const key = "0f3c9a1b2d4e5f60718293a4b5c6d7e8" // an engine key: 32 lowercase hex

// hooks records the callbacks M-Pesa makes to Taskiem.
type hooks struct {
	*httptest.Server
	mu  sync.Mutex
	got []delivery
}

type delivery struct {
	trigger string
	query   url.Values
	body    []byte
}

func newHooks(t *testing.T) *hooks {
	h := &hooks{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		parts := strings.Split(r.URL.Path, "/")
		h.mu.Lock()
		h.got = append(h.got, delivery{trigger: parts[len(parts)-1], query: r.URL.Query(), body: b})
		h.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *hooks) take(t *testing.T) []delivery {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.got
	h.got = nil
	return out
}

type env struct {
	fake  *fakedaraja.Server
	hooks *hooks
	conn  *connector.Connector
	now   time.Time
	creds map[string]string
}

func setup(t *testing.T) *env {
	t.Helper()
	e := &env{fake: fakedaraja.New(t), hooks: newHooks(t), now: time.Date(2026, 10, 7, 7, 15, 20, 0, time.UTC)}
	e.fake.Now = func() time.Time { return e.now }
	e.conn = newConnector(Options{BaseURL: e.fake.URL}, func() time.Time { return e.now }, rand.Reader)
	e.creds = map[string]string{"consumer_key": "ck_test", "consumer_secret": "cs_test", "environment": "sandbox",
		"shortcode": "174379", "passkey": "pk_test", "b2c_shortcode": "600997", "initiator_name": "testapi",
		"initiator_password": "Safaricom999!*!", "certificate": certificate(t, e.fake),
		"hooks_url": e.hooks.URL + "/hooks/7b0c/connectors/mpesa@1?connection=main", "callback_token": "cbtok-0123456789abcdef"}
	return e
}

// certificate is a self-signed certificate for the fake's key, standing in
// for the one Safaricom publishes on the portal.
func certificate(t *testing.T, f *fakedaraja.Server) string {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake M-Pesa"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &f.Key.PublicKey, f.Key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func (e *env) call(action string, input map[string]any) (map[string]any, error) {
	r, err := e.conn.Actions[action].Execute(context.Background(), connector.Request{Input: input, Credentials: e.creds,
		HTTP: e.fake.Client(), IdempotencyKey: key, Attempt: 1})
	if err != nil {
		return nil, err
	}
	return r.Output.(map[string]any), nil
}

// event evaluates a delivery the way ingest does: verify the token, then
// the trigger's expressions over body and query (token and env hidden).
func event(t *testing.T, c *connector.Connector, d delivery, secret string) (name, dedup, corr string, err error) {
	t.Helper()
	spec, ok := c.Manifest.Triggers[d.trigger]
	if !ok {
		t.Fatalf("no trigger %q", d.trigger)
	}
	if err := connector.VerifyQuerySecret(spec.Verify, secret, d.query); err != nil {
		return "", "", "", err
	}
	body, err := expr.DecodeJSON(d.body)
	if err != nil {
		t.Fatal(err)
	}
	q := map[string]any{}
	for k, v := range d.query {
		if k != "env" && k != spec.Verify.Query {
			q[k] = v[0]
		}
	}
	act := map[string]any{"body": body, "headers": map[string]any{}, "query": q, "item": nil}
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	out := make([]string, 3)
	for i, src := range []string{spec.EventType, spec.Dedup, spec.Correlation} {
		if src == "" {
			continue
		}
		v, err := e.Eval(src, act)
		if err != nil {
			t.Fatalf("%s: %s: %v", d.trigger, src, err)
		}
		out[i], _ = v.(string)
	}
	return out[0], out[1], out[2], nil
}

func kind(err error) effects.ErrorKind { return effects.Classify(err) }

func TestManifestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("mpesa@1")
	if !ok {
		t.Fatal("mpesa@1 not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "api.safaricom.co.ke" || h[1] != "sandbox.safaricom.co.ke" {
		t.Errorf("hosts %v", h)
	}
	cl := &client{live: "L", sandbox: "S"}
	if cl.base(connector.Request{Credentials: map[string]string{"environment": " Sandbox"}}) != "S" || cl.base(connector.Request{}) != "L" {
		t.Error("environment selection")
	}
}

func TestTokenCachedExpiryAndReplacement(t *testing.T) {
	e := setup(t)
	for i := 0; i < 3; i++ {
		if _, err := e.call("check_credentials", nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.fake.TokensIssued(); n != 1 {
		t.Errorf("one token should serve three calls, got %d", n)
	}
	// expires_in 3599 less the minute's margin: renewed before it lapses.
	e.now = e.now.Add(59 * time.Minute)
	if _, err := e.call("check_credentials", nil); err != nil {
		t.Fatal(err)
	}
	if n := e.fake.TokensIssued(); n != 2 {
		t.Errorf("expired token not renewed: %d", n)
	}
	// Another process took a new token, invalidating ours: Daraja answers
	// 404.001.03 and the connector renews once.
	e.fake.RevokeTokens()
	if _, err := e.call("stk_query", map[string]any{"checkout_request_id": "ws_CO_none"}); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "CheckoutRequestID") {
		t.Errorf("after revocation: %v", err)
	}
	if n := e.fake.TokensIssued(); n != 3 {
		t.Errorf("revoked token not renewed: %d", n)
	}
	// Wrong keys: fatal, nothing sent.
	e.creds["consumer_secret"] = "nope"
	e.conn = New(Options{BaseURL: e.fake.URL})
	if _, err := e.call("check_credentials", nil); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "consumer_key") {
		t.Errorf("wrong keys: %v", err)
	}
}

func TestSTKPushAndQuery(t *testing.T) {
	e := setup(t)
	in := map[string]any{"amount": 150000, "phone": "+254 708 374149", "account_reference": "INV-17", "description": "Order 17"}
	out, err := e.call("stk_push", in)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := out["checkout_request_id"].(string)
	if !strings.HasPrefix(id, "ws_CO_") || out["response_code"] != "0" {
		t.Fatalf("push: %v", out)
	}
	sent := e.fake.Requests[len(e.fake.Requests)-1].Body
	wantPW := base64.StdEncoding.EncodeToString([]byte("174379pk_test20261007101520")) // EAT: UTC+3
	if sent["Password"] != wantPW || sent["Timestamp"] != "20261007101520" || sent["Amount"] != "1500" || sent["PartyA"] != "254708374149" ||
		sent["PhoneNumber"] != "254708374149" || sent["PartyB"] != "174379" || sent["TransactionType"] != "CustomerPayBillOnline" {
		t.Errorf("request %v", sent)
	}
	cb, _ := url.Parse(sent["CallBackURL"].(string))
	if cb.Path != "/hooks/7b0c/connectors/mpesa@1/stk_callback" || cb.Query().Get("connection") != "main" || cb.Query().Get("token") != "cbtok-0123456789abcdef" {
		t.Errorf("callback URL %s", cb)
	}

	// The customer has not answered: the query says pending; a second
	// prompt to the same phone is refused before anything is sent.
	if q, err := e.call("stk_query", map[string]any{"checkout_request_id": id}); err != nil || q["status"] != "pending" {
		t.Errorf("pending query: %v %v", q, err)
	}
	if _, err := e.call("stk_push", in); kind(err) != effects.KindNotSent {
		t.Errorf("locked subscriber: %v", err)
	}

	e.fake.Flush(e.hooks.Client())
	got := e.hooks.take(t)
	if len(got) != 1 {
		t.Fatalf("callbacks: %d", len(got))
	}
	name, dedup, corr, err := event(t, e.conn, got[0], e.creds["callback_token"])
	if err != nil || name != "stk.completed" || dedup != id || corr != id {
		t.Errorf("stk callback: %q %q %q %v", name, dedup, corr, err)
	}
	if q, err := e.call("stk_query", map[string]any{"checkout_request_id": id}); err != nil || q["status"] != "completed" || q["result_code"] != "0" {
		t.Errorf("completed query: %v %v", q, err)
	}

	// A customer who cancels.
	e.fake.Customer["254711000001"] = 1032
	out, err = e.call("stk_push", map[string]any{"amount": 100, "phone": "254711000001", "account_reference": "INV-18",
		"transaction_type": "CustomerBuyGoodsOnline", "party_b": "5550001"})
	if err != nil {
		t.Fatal(err)
	}
	e.fake.Flush(e.hooks.Client())
	got = e.hooks.take(t)
	if name, _, _, _ := event(t, e.conn, got[0], e.creds["callback_token"]); name != "stk.failed" {
		t.Errorf("cancelled: %q", name)
	}
	if q, _ := e.call("stk_query", map[string]any{"checkout_request_id": out["checkout_request_id"]}); q["status"] != "cancelled" {
		t.Errorf("cancelled query: %v", q)
	}
}

func TestSTKPushRefusals(t *testing.T) {
	e := setup(t)
	base := map[string]any{"amount": 100, "phone": "254708374149", "account_reference": "INV-1"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	// Checked before anything is sent.
	for _, in := range []map[string]any{with("amount", 150), with("amount", 99.5), with("phone", "0708374149"),
		with("account_reference", "THIRTEEN-CHAR"), with("transaction_type", "Other")} {
		if _, err := e.call("stk_push", in); kind(err) != effects.KindFatal {
			t.Errorf("%v: %v", in, err)
		}
	}
	if len(e.fake.Requests) != 0 {
		t.Errorf("refused inputs reached Daraja: %d requests", len(e.fake.Requests))
	}
	e.creds["passkey"] = "wrong"
	if _, err := e.call("stk_push", base); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "Wrong credentials") {
		t.Errorf("wrong passkey: %v", err)
	}
	e.creds["passkey"] = "pk_test"
	e.fake.FailNext("/mpesa/stkpush/v1/processrequest", 500, "500.003.02", "Error Occurred: Spike Arrest Violation")
	if _, err := e.call("stk_push", base); kind(err) != effects.KindNotSent {
		t.Errorf("spike arrest: %v", err)
	}
	// The answer is lost: Safaricom keeps no reference to look it up by,
	// so the outcome is unknown and the unsafe write parks.
	e.fake.LoseNextAnswer("/mpesa/stkpush/v1/processrequest")
	_, err := e.call("stk_push", base)
	if kind(err) != effects.KindUnknownOutcome || effects.AfterError(e.conn.Manifest.Actions["stk_push"].Class, kind(err)) != effects.Park {
		t.Errorf("lost answer: %v", err)
	}
	delete(e.creds, "hooks_url")
	if _, err := e.call("stk_push", with("phone", "254700000009")); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "hooks_url") {
		t.Errorf("no hooks_url: %v", err)
	}
}

var b2cIn = map[string]any{"amount": 1000000, "phone": "254705912645", "remarks": "October salary", "command_id": "SalaryPayment",
	"occasion": "Payroll", "originator_conversation_id": key}

func TestB2CPaymentResultAndDuplicates(t *testing.T) {
	e := setup(t)
	out, err := e.call("b2c_payment", b2cIn)
	if err != nil {
		t.Fatal(err)
	}
	if out["originator_conversation_id"] != key || out["status"] != "accepted" || out["duplicate"] != false || out["conversation_id"] == "" {
		t.Errorf("b2c: %v", out)
	}
	sent := e.fake.Requests[len(e.fake.Requests)-1].Body
	if sent["Amount"] != "10000" || sent["PartyA"] != "600997" || sent["PartyB"] != "254705912645" || sent["Occassion"] != "Payroll" || sent["InitiatorName"] != "testapi" { //nolint:misspell // Daraja spells it so
		t.Errorf("request %v", sent)
	}
	ru, _ := url.Parse(sent["ResultURL"].(string))
	tu, _ := url.Parse(sent["QueueTimeOutURL"].(string))
	if !strings.HasSuffix(ru.Path, "/b2c_result") || ru.Query().Get("ref") != key || !strings.HasSuffix(tu.Path, "/queue_timeout") || tu.Query().Get("kind") != "b2c" {
		t.Errorf("urls %s %s", ru, tu)
	}
	e.fake.Flush(e.hooks.Client())
	got := e.hooks.take(t)
	name, _, corr, err := event(t, e.conn, got[0], e.creds["callback_token"])
	if err != nil || name != "b2c.completed" || corr != key {
		t.Errorf("result: %q %q %v", name, corr, err)
	}

	// The same key again (a resend after a lost answer): M-Pesa refuses
	// the id, and the step reports the payment already accepted.
	out, err = e.call("b2c_payment", b2cIn)
	if err != nil || out["duplicate"] != true || out["status"] != "accepted" {
		t.Errorf("duplicate: %v %v", out, err)
	}
	if e.fake.Pending() != 0 {
		t.Error("a duplicate must not pay")
	}
}

// An unknown outcome is settled by resending under the same key: the
// idempotent write's reconciliation.
func TestB2CLostAnswerResendsSafely(t *testing.T) {
	e := setup(t)
	e.fake.LoseNextAnswer("/mpesa/b2c/v3/paymentrequest")
	_, err := e.call("b2c_payment", b2cIn)
	class := e.conn.Manifest.Actions["b2c_payment"].Class
	if kind(err) != effects.KindUnknownOutcome || effects.AfterError(class, kind(err)) != effects.Retry {
		t.Fatalf("lost answer: %v", err)
	}
	out, err := e.call("b2c_payment", b2cIn)
	if err != nil || out["duplicate"] != true {
		t.Fatalf("resend: %v %v", out, err)
	}
	e.fake.Flush(e.hooks.Client())
	if got := e.hooks.take(t); len(got) != 1 {
		t.Errorf("paid %d times", len(got))
	}
	// Refusals Daraja documents as "try again" were never processed.
	e.fake.FailNext("/mpesa/b2c/v3/paymentrequest", 500, "500.003.03", "Quota Violation")
	if _, err := e.call("b2c_payment", map[string]any{"amount": 100, "phone": "254705912645", "remarks": "ok", "originator_conversation_id": "aa"}); kind(err) != effects.KindNotSent {
		t.Errorf("quota: %v", err)
	}
}

func TestSecurityCredential(t *testing.T) {
	e := setup(t)
	// Encrypted by the connector with the certificate: the fake decrypts it.
	if _, err := e.call("account_balance", nil); err != nil {
		t.Fatal(err)
	}
	e.fake.Flush(e.hooks.Client())
	if name, _, _, _ := event(t, e.conn, e.hooks.take(t)[0], e.creds["callback_token"]); name != "balance.completed" {
		t.Errorf("encrypted credential: %q", name)
	}
	// Given already encrypted.
	e.fake.Credential = "PRE-ENCRYPTED=="
	e.creds["security_credential"] = "PRE-ENCRYPTED=="
	if _, err := e.call("account_balance", nil); err != nil {
		t.Fatal(err)
	}
	// A wrong password is reported by M-Pesa in the result (2001).
	delete(e.creds, "security_credential")
	e.creds["initiator_password"] = "wrong"
	if _, err := e.call("b2c_payment", b2cIn); err != nil {
		t.Fatal(err)
	}
	e.fake.Flush(e.hooks.Client())
	got := e.hooks.take(t)
	if name, _, _, _ := event(t, e.conn, got[len(got)-1], e.creds["callback_token"]); name != "b2c.failed" {
		t.Errorf("wrong password: %q", name)
	}
	// Missing pieces never reach Daraja.
	for _, k := range []string{"initiator_name", "certificate"} {
		c := map[string]string{}
		for a, b := range e.creds {
			c[a] = b
		}
		delete(c, k)
		e2 := *e
		e2.creds = c
		if _, err := e2.call("account_balance", nil); kind(err) != effects.KindFatal {
			t.Errorf("without %s: %v", k, err)
		}
	}
	if _, err := publicKey("not pem"); err == nil {
		t.Error("bad PEM accepted")
	}
}

func TestStatusBalanceReversalAndTimeout(t *testing.T) {
	e := setup(t)
	out, err := e.call("transaction_status", map[string]any{"transaction_id": "MBN31H462N"})
	if err != nil || out["correlation"] != "MBN31H462N" || out["conversation_id"] == "" {
		t.Fatalf("status: %v %v", out, err)
	}
	sent := e.fake.Requests[len(e.fake.Requests)-1].Body
	if sent["CommandID"] != "TransactionStatusQuery" || sent["PartyA"] != "600997" || sent["IdentifierType"] != "4" || sent["TransactionID"] != "MBN31H462N" {
		t.Errorf("status request %v", sent)
	}
	if _, err := e.call("transaction_status", nil); kind(err) != effects.KindFatal {
		t.Errorf("status without id: %v", err)
	}
	bal, err := e.call("account_balance", map[string]any{"party_a": "600000"})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := e.call("reversal", map[string]any{"transaction_id": "TJ71234567", "amount": 100})
	if err != nil || rev["correlation"] != "TJ71234567" {
		t.Fatalf("reversal: %v %v", rev, err)
	}
	sent = e.fake.Requests[len(e.fake.Requests)-1].Body
	if sent["RecieverIdentifierType"] != "11" || sent["ReceiverParty"] != "174379" || sent["Amount"] != "1" {
		t.Errorf("reversal request %v", sent)
	}
	if _, err := e.call("reversal", map[string]any{"transaction_id": "NOPE", "amount": 100}); err != nil {
		t.Fatal(err)
	}
	e.fake.Flush(e.hooks.Client())
	got := e.hooks.take(t)
	want := []struct{ name, corr string }{{"status.completed", "MBN31H462N"}, {"balance.completed", bal["correlation"].(string)},
		{"reversal.completed", "TJ71234567"}, {"reversal.failed", "NOPE"}}
	if len(got) != len(want) {
		t.Fatalf("callbacks: %d", len(got))
	}
	for i, w := range want {
		name, dedup, corr, err := event(t, e.conn, got[i], e.creds["callback_token"])
		if err != nil || name != w.name || corr != w.corr || dedup == "" {
			t.Errorf("%d: %q %q %q %v, want %v", i, name, dedup, corr, err, w)
		}
	}

	// M-Pesa gives up waiting: the timeout URL is called instead.
	if _, err := e.call("b2c_payment", b2cIn); err != nil {
		t.Fatal(err)
	}
	sent = e.fake.Requests[len(e.fake.Requests)-1].Body
	e.fake.TimeoutNext(sent["QueueTimeOutURL"].(string))
	e.fake.Flush(e.hooks.Client())
	got = e.hooks.take(t)
	name, dedup, corr, err := event(t, e.conn, got[0], e.creds["callback_token"])
	if err != nil || got[0].trigger != "queue_timeout" || name != "timeout.b2c" || corr != key || dedup != "" {
		t.Errorf("timeout: %s %q %q %q %v", got[0].trigger, name, dedup, corr, err)
	}
}

func TestRegisterC2BURLs(t *testing.T) {
	e := setup(t)
	e.fake.Production = true
	out, err := e.call("register_c2b_urls", map[string]any{"response_type": "Cancelled"})
	if err != nil || out["registered"] != true {
		t.Fatalf("register: %v %v", out, err)
	}
	sent := e.fake.Requests[len(e.fake.Requests)-1].Body
	if !strings.Contains(sent["ConfirmationURL"].(string), "/c2b_confirmation?") || !strings.Contains(sent["ValidationURL"].(string), "/c2b_validation?") || sent["ShortCode"] != "174379" {
		t.Errorf("request %v", sent)
	}
	out, err = e.call("register_c2b_urls", nil)
	if err != nil || out["already_registered"] != true {
		t.Errorf("again: %v %v", out, err)
	}
	if _, err := e.call("register_c2b_urls", map[string]any{"response_type": "Maybe"}); kind(err) != effects.KindFatal {
		t.Errorf("bad response type: %v", err)
	}
}

// TestCallbacks checks every trigger against the bodies Safaricom
// documents, and that deliveries without the right token are refused.
func TestCallbacks(t *testing.T) {
	raw, err := os.ReadFile("testdata/callbacks.json")
	if err != nil {
		t.Fatal(err)
	}
	var docs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatal(err)
	}
	c := New(Options{})
	tok := url.Values{"token": {"s3cret"}}
	with := func(extra url.Values) url.Values {
		q := url.Values{"token": {"s3cret"}, "connection": {"main"}}
		for k, v := range extra {
			q[k] = v
		}
		return q
	}
	for _, tc := range []struct {
		fixture, trigger string
		query            url.Values
		name, dedup      string
		corr             string
	}{
		{"stk_success", "stk_callback", tok, "stk.completed", "ws_CO_191220191020363925", "ws_CO_191220191020363925"},
		{"stk_cancelled", "stk_callback", tok, "stk.failed", "ws_CO_21072024125243250722943992", "ws_CO_21072024125243250722943992"},
		{"b2c_success", "b2c_result", with(url.Values{"ref": {key}, "kind": {"b2c"}}), "b2c.completed", "AG_20240706_2010364430d9bbdaf872:0", key},
		{"b2c_failed", "b2c_result", tok, "b2c.failed", "AG_20240707_201062f6f6f5804f7a33:2001", "53e3-4aa8-9fe0-8fb5e4092cdd3544366"},
		{"balance", "balance_result", tok, "balance.completed", "AG_20200206_00005e091a8ec6b9eac5:0", "16917-22577599-3"},
		{"status", "status_result", with(url.Values{"ref": {"MBN31H462N"}}), "status.completed", "AG_20180223_0000493344ae97d86f75:0", "MBN31H462N"},
		{"reversal_failed", "reversal_result", tok, "reversal.failed", "AG_20211114_2010573069aefb6b625a:R000002", "3124-481d-b706-10bdd6fbc8e21792398"},
		{"c2b_confirmation", "c2b_confirmation", tok, "c2b.confirmation", "RKL51ZDR4F", "Sample Transaction"},
		{"c2b_buygoods", "c2b_confirmation", tok, "c2b.confirmation", "RKL51ZDR4G", "RKL51ZDR4G"},
		{"c2b_confirmation", "c2b_validation", tok, "c2b.validation", "validation:RKL51ZDR4F", "Sample Transaction"},
	} {
		d := delivery{trigger: tc.trigger, query: tc.query, body: docs[tc.fixture]}
		name, dedup, corr, err := event(t, c, d, "s3cret")
		if err != nil || name != tc.name || dedup != tc.dedup || corr != tc.corr {
			t.Errorf("%s on %s: %q %q %q %v", tc.fixture, tc.trigger, name, dedup, corr, err)
		}
		if spec := c.Manifest.Triggers[tc.trigger]; len(spec.Events) > 0 && !contains(spec.Events, name) {
			t.Errorf("%s: event %q not declared", tc.trigger, name)
		}
		// Daraja signs nothing: without the token, or with another, refused.
		for _, q := range []url.Values{{}, {"token": {"guess"}}} {
			if _, _, _, err := event(t, c, delivery{trigger: tc.trigger, query: q, body: d.body}, "s3cret"); !errors.Is(err, connector.ErrBadSignature) {
				t.Errorf("%s accepted with %v", tc.trigger, q)
			}
		}
	}
	// C2B notifications are answered as Safaricom documents.
	for _, tr := range []string{"c2b_confirmation", "c2b_validation"} {
		if a := c.Manifest.Triggers[tr].Ack; a == nil || a.Status != 200 || a.Body != `{"ResultCode":"0","ResultDesc":"Accepted"}` {
			t.Errorf("%s ack %+v", tr, a)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
