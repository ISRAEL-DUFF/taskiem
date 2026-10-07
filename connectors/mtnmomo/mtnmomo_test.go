package mtnmomo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/mtnmomo/internal/fakemomo"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

const key = "9f86d081884c7d659a2feaa0c55ad015" // an engine key: 32 lowercase hex

// ref is key as the version-4 UUID sent as X-Reference-Id.
const ref = "9f86d081-884c-4d65-9a2f-eaa0c55ad015"

type hooks struct {
	*httptest.Server
	mu  sync.Mutex
	got []delivery
}

type delivery struct {
	method, path string
	body         []byte
}

func newHooks(t *testing.T) *hooks {
	h := &hooks{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.got = append(h.got, delivery{r.Method, r.URL.Path, b})
		h.mu.Unlock()
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *hooks) take() []delivery {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.got
	h.got = nil
	return out
}

type env struct {
	fake  *fakemomo.Server
	hooks *hooks
	conn  *connector.Connector
	now   time.Time
	creds map[string]string
}

func setup(t *testing.T) *env {
	t.Helper()
	e := &env{fake: fakemomo.New(t), hooks: newHooks(t), now: time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)}
	e.fake.Now = func() time.Time { return e.now }
	e.fake.CallbackHost = "127.0.0.1"
	e.conn = newConnector(Options{BaseURL: e.fake.URL}, func() time.Time { return e.now })
	e.creds = map[string]string{"api_user": e.fake.APIUser, "api_key": e.fake.APIKey, "collection_subscription_key": "sub-coll",
		"disbursement_subscription_key": "sub-disb", "remittance_subscription_key": "sub-remit", "environment": "sandbox",
		"hooks_url": e.hooks.URL + "/hooks/7b0c/connectors/mtnmomo@1", "hooks_connection": "uganda", "callback_token": "cbtok-abcdef0123456789"}
	return e
}

func (e *env) call(action string, input map[string]any) (map[string]any, error) {
	r, err := e.conn.Actions[action].Execute(context.Background(), connector.Request{Input: input, Credentials: e.creds,
		HTTP: e.fake.Client(), IdempotencyKey: key, Attempt: 1})
	if err != nil {
		return nil, err
	}
	return r.Output.(map[string]any), nil
}

func kind(err error) effects.ErrorKind { return effects.Classify(err) }

func last(f *fakemomo.Server) fakemomo.Request { return f.Requests[len(f.Requests)-1] }

// event checks a callback the way ingest does for the path form: the last
// segment must be the token; then the trigger's expressions over the body.
func event(t *testing.T, c *connector.Connector, d delivery, secret string) (name, dedup, corr string, err error) {
	t.Helper()
	segs := strings.Split(strings.TrimPrefix(d.path, "/"), "/")
	// hooks/<tenant>/connectors/mtnmomo@1/<trigger>/<env>/<connection>/<token>
	if len(segs) != 8 || segs[2] != "connectors" || segs[3] != "mtnmomo@1" {
		t.Fatalf("callback path %s", d.path)
	}
	spec := c.Manifest.Triggers[segs[4]]
	if spec.Verify == nil || spec.Verify.Scheme != "path_secret" {
		t.Fatalf("trigger %s", segs[4])
	}
	if err := connector.VerifyPathSecret(secret, segs[7]); err != nil {
		return "", "", "", err
	}
	body, err := expr.DecodeJSON(d.body)
	if err != nil {
		t.Fatal(err)
	}
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	act := map[string]any{"body": body, "headers": map[string]any{}, "query": map[string]any{}, "item": nil}
	out := make([]string, 3)
	for i, src := range []string{spec.EventType, spec.Dedup, spec.Correlation} {
		v, err := e.Eval(src, act)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		out[i], _ = v.(string)
	}
	return out[0], out[1], out[2], nil
}

func TestManifestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("mtnmomo@1")
	if !ok {
		t.Fatal("mtnmomo@1 not registered")
	}
	if h := c.Manifest.Hosts(); len(h) != 2 || h[0] != "proxy.momoapi.mtn.com" || h[1] != "sandbox.momodeveloper.mtn.com" {
		t.Errorf("hosts %v", h)
	}
}

func TestReferenceID(t *testing.T) {
	got, err := referenceID(key)
	if err != nil || got != ref {
		t.Fatalf("%q %v", got, err)
	}
	if again, _ := referenceID(strings.ToUpper(ref)); again != ref {
		t.Errorf("a UUID is kept: %q", again)
	}
	for _, bad := range []string{"", "xyz", key[:31], key + "0"} {
		if _, err := referenceID(bad); kind(err) != effects.KindFatal {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if major(150050, 2) != "1500.50" || major(100000, 2) != "1000" || major(5000, 0) != "5000" {
		t.Error("amount rendering")
	}
}

func TestTokensPerProductCachedAndRenewed(t *testing.T) {
	e := setup(t)
	for i := 0; i < 2; i++ {
		if _, err := e.call("get_balance", map[string]any{"product": "collection"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.call("get_balance", map[string]any{"product": "disbursement"}); err != nil {
		t.Fatal(err)
	}
	if n := e.fake.TokensIssued(); n != 2 {
		t.Errorf("one token per product: %d", n)
	}
	if h := last(e.fake).Header; h.Get("Ocp-Apim-Subscription-Key") != "sub-disb" || h.Get("X-Target-Environment") != "sandbox" {
		t.Errorf("headers %v", h)
	}
	e.now = e.now.Add(59*time.Minute + time.Second) // past expires_in less the minute's margin
	if _, err := e.call("get_balance", map[string]any{"product": "collection"}); err != nil {
		t.Fatal(err)
	}
	if n := e.fake.TokensIssued(); n != 3 {
		t.Errorf("expired token not renewed: %d", n)
	}
	// MTN revokes early: one 401, a new token, the call again.
	e.fake.ExpireTokens()
	if _, err := e.call("get_balance", map[string]any{"product": "collection"}); err != nil {
		t.Errorf("after revocation: %v", err)
	}
	// Wrong credentials and keys are fatal.
	e.creds["api_key"] = "wrong"
	e.conn = New(Options{BaseURL: e.fake.URL})
	if _, err := e.call("get_balance", nil); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "subscription key") {
		t.Errorf("wrong key: %v", err)
	}
	e.creds["api_key"] = e.fake.APIKey
	e.creds["collection_subscription_key"] = "wrong"
	if _, err := e.call("get_balance", nil); kind(err) != effects.KindFatal {
		t.Errorf("wrong subscription key: %v", err)
	}
	delete(e.creds, "collection_subscription_key")
	if _, err := e.call("get_balance", map[string]any{"product": "collection"}); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "collection_subscription_key") {
		t.Errorf("no key: %v", err)
	}
}

func TestReads(t *testing.T) {
	e := setup(t)
	out, err := e.call("get_balance", nil)
	if err != nil || out["product"] != "collection" || out["available_balance"] != int64(100050) || out["currency"] != "EUR" {
		t.Errorf("balance %v %v", out, err)
	}
	e.fake.Balance = "12.3456"
	if out, _ := e.call("get_balance", nil); out["available_balance"] != int64(1234) {
		t.Errorf("balance floors: %v", out)
	}
	out, err = e.call("validate_account_holder", map[string]any{"msisdn": "+46 733 123454"})
	if err != nil || out["active"] != true || out["msisdn"] != "46733123454" {
		t.Errorf("active %v %v", out, err)
	}
	if !strings.HasSuffix(last(e.fake).Path, "/collection/v1_0/accountholder/msisdn/46733123454/active") {
		t.Errorf("path %s", last(e.fake).Path)
	}
	if out, _ := e.call("validate_account_holder", map[string]any{"msisdn": "46733123451", "product": "disbursement"}); out["active"] != false {
		t.Errorf("inactive %v", out)
	}
	out, err = e.call("get_account_holder_name", map[string]any{"msisdn": "46733123454"})
	if err != nil || out["given_name"] != "Sand" || out["family_name"] != "Box" {
		t.Errorf("name %v %v", out, err)
	}
	if _, err := e.call("get_account_holder_name", map[string]any{"msisdn": "46733123450"}); kind(err) != effects.KindFatal {
		t.Errorf("unknown holder: %v", err)
	}
	if _, err := e.call("validate_account_holder", map[string]any{"msisdn": "07xx"}); kind(err) != effects.KindFatal {
		t.Errorf("bad msisdn: %v", err)
	}
	e.creds["environment"] = "production"
	if _, err := e.call("get_balance", nil); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "target_environment") {
		t.Errorf("production without target: %v", err)
	}
	e.creds["target_environment"] = "mtnuganda"
	if _, err := e.call("get_balance", nil); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "NOT_ALLOWED_TARGET_ENVIRONMENT") {
		t.Errorf("wrong target: %v", err)
	}
}

var rtpIn = map[string]any{"amount": 150050, "msisdn": "46733123454", "payer_message": "Order 17", "payee_note": "Shop", "reference_id": key}

func TestRequestToPayAndCallback(t *testing.T) {
	e := setup(t)
	out, err := e.call("request_to_pay", rtpIn)
	if err != nil {
		t.Fatal(err)
	}
	if out["reference_id"] != ref || out["status"] != "PENDING" || out["amount"] != int64(150050) || out["external_id"] != ref {
		t.Errorf("rtp %v", out)
	}
	req := last(e.fake)
	if req.Header.Get("X-Reference-Id") != ref || req.Body["amount"] != "1500.50" || req.Body["currency"] != "EUR" || req.Body["externalId"] != ref {
		t.Errorf("request %v %v", req.Header, req.Body)
	}
	cb := req.Header.Get("X-Callback-Url")
	if !strings.HasSuffix(cb, "/hooks/7b0c/connectors/mtnmomo@1/payment_callback/prod/uganda/cbtok-abcdef0123456789") || strings.Contains(cb, "?") {
		t.Errorf("callback url %s", cb)
	}
	if got, _ := e.call("get_payment", map[string]any{"reference_id": ref}); got["status"] != "PENDING" {
		t.Errorf("pending: %v", got)
	}
	e.fake.Flush(e.hooks.Client())
	got := e.hooks.take()
	if len(got) != 1 || got[0].method != http.MethodPut {
		t.Fatalf("callbacks %v", got)
	}
	name, dedup, corr, err := event(t, e.conn, got[0], e.creds["callback_token"])
	if err != nil || name != "requesttopay.SUCCESSFUL" || corr != ref || dedup != ref+":SUCCESSFUL" {
		t.Errorf("event %q %q %q %v", name, dedup, corr, err)
	}
	if _, _, _, err := event(t, e.conn, delivery{got[0].method, strings.Replace(got[0].path, "cbtok-abcdef0123456789", "guess", 1), got[0].body}, e.creds["callback_token"]); !errors.Is(err, connector.ErrBadSignature) {
		t.Errorf("forged callback accepted: %v", err)
	}
	// get_payment by the engine's key (as a reconcile passes it) or the UUID.
	for _, id := range []string{key, ref} {
		p, err := e.call("get_payment", map[string]any{"reference_id": id})
		if err != nil || p["status"] != "SUCCESSFUL" || p["financial_transaction_id"] == "" || p["amount"] != int64(150050) || p["msisdn"] != "46733123454" {
			t.Errorf("get %s: %v %v", id, p, err)
		}
	}

	// A payer who rejects: FAILED with MTN's reason.
	in := map[string]any{"amount": 500, "msisdn": "46733123451", "reference_id": "00000000000000000000000000000001"}
	if _, err := e.call("request_to_pay", in); err != nil {
		t.Fatal(err)
	}
	e.fake.Flush(e.hooks.Client())
	if name, _, _, _ := event(t, e.conn, e.hooks.take()[0], e.creds["callback_token"]); name != "requesttopay.FAILED" {
		t.Errorf("rejected: %q", name)
	}
	p, _ := e.call("get_payment", map[string]any{"reference_id": "00000000000000000000000000000001"})
	if p["status"] != "FAILED" || p["reason_code"] != "APPROVAL_REJECTED" {
		t.Errorf("rejected status %v", p)
	}
	// Without hooks_url no callback is asked for.
	delete(e.creds, "hooks_url")
	if _, err := e.call("request_to_pay", map[string]any{"amount": 100, "msisdn": "46733123454", "reference_id": "00000000000000000000000000000002"}); err != nil {
		t.Fatal(err)
	}
	if last(e.fake).Header.Get("X-Callback-Url") != "" {
		t.Error("callback asked for without hooks_url")
	}
}

func TestDuplicateReferenceReportsTheOriginal(t *testing.T) {
	e := setup(t)
	if _, err := e.call("request_to_pay", rtpIn); err != nil {
		t.Fatal(err)
	}
	// The same key again: MTN answers 409; the connector reads it back.
	out, err := e.call("request_to_pay", rtpIn)
	if err != nil || out["reference_id"] != ref || out["status"] != "PENDING" {
		t.Errorf("duplicate: %v %v", out, err)
	}
	if n := len(e.fake.Requests); e.fake.Requests[n-2].Method != http.MethodPost || e.fake.Requests[n-1].Method != http.MethodGet {
		t.Error("duplicate not read back")
	}
	// A transfer whose original failed: the step fails with the reason.
	tin := map[string]any{"amount": 2000, "msisdn": "46733123455", "reference_id": key}
	if _, err := e.call("transfer", tin); err != nil {
		t.Fatal(err)
	}
	e.fake.Flush(e.hooks.Client())
	if _, err := e.call("transfer", tin); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "NOT_ENOUGH_FUNDS") {
		t.Errorf("failed original: %v", err)
	}
	// MTN says duplicate but has nothing under the reference: never assume.
	e.fake.FailNext("/v1_0/transfer", http.StatusConflict, "RESOURCE_ALREADY_EXIST", "Duplicated reference id. Creation of resource failed.")
	if _, err := e.call("transfer", map[string]any{"amount": 2000, "msisdn": "46733123454", "reference_id": "11111111111111111111111111111111"}); kind(err) != effects.KindUnknownOutcome {
		t.Errorf("duplicate, not found: %v", err)
	}
}

// TestUnknownOutcomeReconciles loses a transfer's answer: the outcome is
// unknown, the reconcile read (as the engine calls it, with the key) finds
// the transfer, and a resend under the same key pays nothing more.
func TestUnknownOutcomeReconciles(t *testing.T) {
	e := setup(t)
	tin := map[string]any{"amount": 250000, "msisdn": "46733123454", "product": "remittance", "payee_note": "Salary", "reference_id": key}
	e.fake.LoseNextAnswer("/v1_0/transfer")
	_, err := e.call("transfer", tin)
	spec := e.conn.Manifest.Actions["transfer"]
	if kind(err) != effects.KindUnknownOutcome || effects.AfterError(spec.Class, kind(err)) != effects.Retry {
		t.Fatalf("lost answer: %v", err)
	}
	if spec.Reconcile != "get_transfer" {
		t.Fatalf("reconcile %q", spec.Reconcile)
	}
	rec, err := e.conn.Actions[spec.Reconcile].Execute(context.Background(), connector.Request{Input: map[string]any{spec.Idempotency.Field: key},
		Credentials: e.creds, HTTP: e.fake.Client(), IdempotencyKey: key})
	if err != nil || rec.Output.(map[string]any)["reference_id"] != ref {
		t.Fatalf("reconcile: %v %v", rec.Output, err)
	}
	// It looked in Disbursement first, then Remittance.
	n := len(e.fake.Requests)
	if e.fake.Requests[n-2].Path != "/disbursement/v1_0/transfer/"+ref || e.fake.Requests[n-1].Path != "/remittance/v1_0/transfer/"+ref {
		t.Errorf("reconcile paths %s %s", e.fake.Requests[n-2].Path, e.fake.Requests[n-1].Path)
	}
	if out, err := e.call("transfer", tin); err != nil || out["status"] != "PENDING" {
		t.Fatalf("resend: %v %v", out, err)
	}
	e.fake.Flush(e.hooks.Client())
	got := e.hooks.take()
	if len(got) != 1 {
		t.Fatalf("paid %d times", len(got))
	}
	if name, _, corr, _ := event(t, e.conn, got[0], e.creds["callback_token"]); name != "transfer.SUCCESSFUL" || corr != ref || !strings.Contains(got[0].path, "/transfer_callback/") {
		t.Errorf("event %q %q", name, corr)
	}
	// Nothing under a key: the reconcile says not found (send again).
	_, err = e.conn.Actions["get_transfer"].Execute(context.Background(), connector.Request{Input: map[string]any{"reference_id": "22222222222222222222222222222222"},
		Credentials: e.creds, HTTP: e.fake.Client()})
	if !errors.Is(err, connector.ErrNotFound) {
		t.Errorf("not found: %v", err)
	}
}

func TestTransferRefusals(t *testing.T) {
	e := setup(t)
	base := map[string]any{"amount": 2000, "msisdn": "46733123454", "reference_id": key}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	// Checked before anything is sent.
	for _, in := range []map[string]any{with("amount", 0), with("amount", 1.5), with("msisdn", "abc"), with("payee_note", "it's"),
		with("payer_message", strings.Repeat("x", 161)), with("currency", "XYZ"), with("product", "collection"), with("reference_id", "nope")} {
		if _, err := e.call("transfer", in); kind(err) != effects.KindFatal {
			t.Errorf("%v: %v", in, err)
		}
	}
	if len(e.fake.Requests) != 0 {
		t.Errorf("refused inputs reached MTN: %d", len(e.fake.Requests))
	}
	// A currency the target environment does not use: MTN answers 500
	// INVALID_CURRENCY; nothing is under the reference, so the step fails.
	if _, err := e.call("transfer", with("currency", "UGX")); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "INVALID_CURRENCY") {
		t.Errorf("currency: %v", err)
	}
	// A callback host other than the API user's.
	e.fake.CallbackHost = "hooks.example.com"
	if _, err := e.call("transfer", base); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "INVALID_CALLBACK_URL_HOST") {
		t.Errorf("callback host: %v", err)
	}
	e.fake.CallbackHost = "127.0.0.1"
	e.fake.FailNext("/v1_0/transfer", http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Service temporary unavailable, try again later")
	if _, err := e.call("transfer", base); kind(err) != effects.KindRetryable {
		t.Errorf("503: %v", err)
	}
	e.fake.FailNext("/v1_0/transfer", http.StatusForbidden, "", "Authorization failed. IP not authorized to use Disbursement API.")
	if _, err := e.call("transfer", base); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "IP not authorized") {
		t.Errorf("403: %v", err)
	}
	e.creds["hooks_connection"] = ""
	if _, err := e.call("transfer", base); kind(err) != effects.KindFatal || !strings.Contains(err.Error(), "hooks_connection") {
		t.Errorf("no connection name: %v", err)
	}
}

// TestCallbacks checks the triggers against the status bodies MTN
// documents (callbacks are "similar to" them).
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
	path := func(trigger, tok string) string {
		return "/hooks/7b0c/connectors/mtnmomo@1/" + trigger + "/prod/uganda/" + tok
	}
	for _, tc := range []struct{ fixture, trigger, name, dedup, corr, reason string }{
		{"rtp_successful", "payment_callback", "requesttopay.SUCCESSFUL", "947354:SUCCESSFUL", "947354", ""},
		{"rtp_payer_not_found", "payment_callback", "requesttopay.FAILED", "947354:FAILED", "947354", "PAYER_NOT_FOUND"},
		{"transfer_successful", "transfer_callback", "transfer.SUCCESSFUL", "83453:SUCCESSFUL", "83453", ""},
		{"transfer_payer_limit", "transfer_callback", "transfer.FAILED", "83453:FAILED", "83453", "PAYER_LIMIT_REACHED"},
		{"transfer_not_enough_funds", "transfer_callback", "transfer.FAILED", "83453:FAILED", "83453", "NOT_ENOUGH_FUNDS"},
	} {
		name, dedup, corr, err := event(t, c, delivery{"PUT", path(tc.trigger, "s3cret"), docs[tc.fixture]}, "s3cret")
		if err != nil || name != tc.name || dedup != tc.dedup || corr != tc.corr {
			t.Errorf("%s: %q %q %q %v", tc.fixture, name, dedup, corr, err)
		}
		if _, _, _, err := event(t, c, delivery{"PUT", path(tc.trigger, "guess"), docs[tc.fixture]}, "s3cret"); !errors.Is(err, connector.ErrBadSignature) {
			t.Errorf("%s: wrong token accepted", tc.fixture)
		}
		// The connector reads the same bodies from the status API.
		var s status
		if err := json.Unmarshal(docs[tc.fixture], &s); err != nil {
			t.Fatal(err)
		}
		out, err := s.output("r")
		if err != nil || out["amount"] != int64(100) || out["currency"] != "UGX" || out["reason_code"] != tc.reason || out["msisdn"] == "" {
			t.Errorf("%s status: %v %v", tc.fixture, out, err)
		}
	}
	for _, tr := range []string{"payment_callback", "transfer_callback"} {
		if a := c.Manifest.Triggers[tr].Ack; a == nil || a.Status != 200 {
			t.Errorf("%s: MTN asks for 200 OK, ack %+v", tr, a)
		}
	}
}
