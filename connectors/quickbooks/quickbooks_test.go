package quickbooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/israel-duff/taskiem/connectors/internal/fixture"
	"github.com/israel-duff/taskiem/engine/connector"
	"github.com/israel-duff/taskiem/engine/conntest"
	"github.com/israel-duff/taskiem/engine/effects"
	"github.com/israel-duff/taskiem/engine/expr"
)

// Every action passes the conformance kit on fixtures shaped after
// Intuit's published examples (docs/integrations/quickbooks.md).
func TestConformance(t *testing.T) {
	s, err := conntest.LoadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	rep := conntest.Run(context.Background(), New(Options{}), s, conntest.Options{})
	for _, r := range rep.Results {
		for _, p := range r.Problems {
			t.Errorf("%s: %s", r.Case, p)
		}
	}
	if len(rep.Uncovered)+len(rep.KeyNotSent)+len(rep.ReadWrites) > 0 {
		t.Errorf("uncovered %v, key not sent %v, reads that write %v", rep.Uncovered, rep.KeyNotSent, rep.ReadWrites)
	}
}

// The books capabilities and events, through their mappings.
func TestCapabilities(t *testing.T) {
	fixture.Capabilities(t, func(base string) *connector.Connector { return New(Options{LiveURL: base}) })
}

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("quickbooks@1")
	m := c.Manifest
	if m.KindOf() != "product" || m.Category != "accounting" || len(m.Capabilities) != 9 {
		t.Errorf("kind %s, category %s, %d capabilities", m.KindOf(), m.Category, len(m.Capabilities))
	}
	if hosts := m.Hosts(); !slices.Equal(hosts, []string{"oauth.platform.intuit.com", "quickbooks.api.intuit.com", "sandbox-quickbooks.api.intuit.com"}) {
		t.Errorf("hosts %v", hosts)
	}
	// Sandbox companies are linked with the development keys (rule 20).
	if m.ClientFor("test") != "quickbooks_sandbox" || m.ClientFor("live") != "quickbooks" || m.ClientFor("") != "quickbooks" ||
		!slices.Equal(m.Clients(), []string{"quickbooks", "quickbooks_sandbox"}) {
		t.Errorf("clients %v", m.Clients())
	}
	if o := m.Auth.OAuth2; o.ClientAuth != "basic" || o.RevokeEncoding != "json" || o.FieldsFromCallback["realm_id"] != "realmId" || o.AccountField != "realm_id" {
		t.Errorf("oauth2 %+v", o)
	}
}

// fakeIntuit is Intuit's token endpoint and API: each refresh hands out a
// new refresh token and spends the one it was given, as Intuit may.
type fakeIntuit struct {
	mu       sync.Mutex
	refresh  string // the one good refresh token
	access   string
	issued   int
	refreshs atomic.Int32
	reject   bool // refuse the API's first call with 401
	calls    []string
}

func (f *fakeIntuit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/token" {
		f.refreshs.Add(1)
		id, secret, ok := r.BasicAuth()
		_ = r.ParseForm()
		if !ok || id != "ABtestclientid" || secret != "test-client-secret" || r.PostForm.Get("client_id") != "" || r.PostForm.Get("grant_type") != "refresh_token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid_client"}`)
			return
		}
		if r.PostForm.Get("refresh_token") != f.refresh {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"Incorrect or invalid refresh token"}`)
			return
		}
		f.issued++
		f.refresh = "AB11-refresh-" + string(rune('a'+f.issued))
		f.access = "eyJ-access-" + string(rune('a'+f.issued))
		_ = json.NewEncoder(w).Encode(map[string]any{"token_type": "bearer", "access_token": f.access, "refresh_token": f.refresh, "expires_in": 3600, "x_refresh_token_expires_in": 8726400})
		return
	}
	f.calls = append(f.calls, r.Header.Get("Authorization"))
	if f.reject || r.Header.Get("Authorization") != "Bearer "+f.access {
		f.reject = false
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"fault":{"error":[{"message":"message=AuthenticationFailed; errorCode=003200; statusCode=401","detail":"Token expired","code":"3200"}],"type":"AUTHENTICATION"}}`)
		return
	}
	_, _ = io.WriteString(w, `{"CompanyInfo":{"CompanyName":"Sandbox Company_NG_1","Country":"NG"}}`)
}

// store is the vault's side of RotateCredentials: one row, locked.
type store struct {
	mu    sync.Mutex
	creds map[string]string
	saves int
}

func (s *store) rotate(_ context.Context, fn connector.RotateFunc) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := map[string]string{}
	for k, v := range s.creds {
		cur[k] = v
	}
	next, err := fn(cur)
	if err != nil {
		return nil, err
	}
	s.creds = next
	s.saves++
	return next, nil
}

func baseCreds() map[string]string {
	return map[string]string{"realm_id": "9341453050298730", "client_id": "ABtestclientid", "client_secret": "test-client-secret", "refresh_token": "AB11-refresh-0"}
}

// A refresh that rotates the refresh token is stored, and concurrent
// workers refresh once: the rest use what the first stored, never the
// spent token.
func TestRotatingRefreshSurvivesConcurrentWorkers(t *testing.T) {
	f := &fakeIntuit{refresh: "AB11-refresh-0"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New(Options{LiveURL: srv.URL, TokenURL: srv.URL + "/token"})
	st := &store{creds: baseCreds()}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st.mu.Lock()
			creds := map[string]string{}
			for k, v := range st.creds {
				creds[k] = v
			}
			st.mu.Unlock()
			_, err := c.Actions["get_company"].Execute(context.Background(), connector.Request{Credentials: creds, HTTP: srv.Client(), Rotate: st.rotate})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.refreshs.Load() != 1 {
		t.Errorf("refreshed %d times, want once", f.refreshs.Load())
	}
	if st.creds["refresh_token"] != f.refresh || st.creds["access_token"] != f.access || st.creds["access_expires_at"] == "" || st.creds["refresh_expires_at"] == "" {
		t.Errorf("stored %v; Intuit's latest is %s", st.creds, f.refresh)
	}
}

// An access token Intuit refuses is refreshed once and the call made
// again; a refresh token Intuit refuses expires the connection.
func TestRefusedTokens(t *testing.T) {
	f := &fakeIntuit{refresh: "AB11-refresh-0"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New(Options{LiveURL: srv.URL, TokenURL: srv.URL + "/token"})
	creds := baseCreds()
	creds["access_token"], creds["access_expires_at"] = "eyJ-revoked", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	f.access = "eyJ-not-that-one"
	st := &store{creds: creds}
	if _, err := c.Actions["get_company"].Execute(context.Background(), connector.Request{Credentials: creds, HTTP: srv.Client(), Rotate: st.rotate}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.calls[0] != "Bearer eyJ-revoked" || f.calls[1] != "Bearer "+f.access {
		t.Errorf("calls %v", f.calls)
	}

	spent := &store{creds: baseCreds()}
	spent.creds["refresh_token"] = "AB11-spent"
	_, err := c.Actions["get_company"].Execute(context.Background(), connector.Request{Credentials: spent.creds, HTTP: srv.Client(), Rotate: spent.rotate})
	if !errors.Is(err, connector.ErrCredentialsRejected) || !errors.Is(err, effects.ErrFatal) || !strings.Contains(err.Error(), "connect QuickBooks again") {
		t.Fatalf("err %v", err)
	}
	if spent.saves != 0 {
		t.Errorf("a refused refresh stored %d times", spent.saves)
	}
}

// Calls go to the company's host for the connection's mode.
func TestModes(t *testing.T) {
	var live, sandbox atomic.Int32
	l := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		live.Add(1)
		_, _ = io.WriteString(w, `{"CompanyInfo":{"CompanyName":"Real"}}`)
	}))
	defer l.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sandbox.Add(1)
		_, _ = io.WriteString(w, `{"CompanyInfo":{"CompanyName":"Sandbox"}}`)
	}))
	defer s.Close()
	c := New(Options{LiveURL: l.URL, TestURL: s.URL})
	creds := baseCreds()
	creds["access_token"], creds["access_expires_at"] = "eyJ-x", "2099-01-01T00:00:00Z"
	creds[connector.ModeKey] = "test"
	out, err := c.Actions["get_company"].Execute(context.Background(), connector.Request{Credentials: creds, HTTP: http.DefaultClient})
	if err != nil || out.Output.(map[string]any)["company_name"] != "Sandbox" || sandbox.Load() != 1 || live.Load() != 0 {
		t.Fatalf("out %v, err %v, live %d, sandbox %d", out.Output, err, live.Load(), sandbox.Load())
	}
}

func TestMoney(t *testing.T) {
	for kobo, want := range map[int64]string{0: "0.00", 5: "0.05", 2500000: "25000.00", 5375050: "53750.50"} {
		if got := amount(kobo).String(); got != want {
			t.Errorf("amount(%d) = %s, want %s", kobo, got, want)
		}
	}
	for in, want := range map[string]int64{"53750": 5375000, "53750.5": 5375050, "0.005": 1, "0.004": 0, "-12.34": -1234, "": 0} {
		if got := kobo(json.Number(in)); got != want {
			t.Errorf("kobo(%q) = %d, want %d", in, got, want)
		}
	}
	if docNumber("INV-2026-0042") != "INV-2026-0042" || docNumber("school-fees-2026-term1-pupil-00042") != "school-fees--f149cada" {
		t.Errorf("docNumber")
	}
	if len(requestID(strings.Repeat("k", 80))) != 50 {
		t.Errorf("requestID")
	}
}

// A delivery of several companies' changes is split into one event each,
// routed by its company, and verified with the app's verifier token.
func TestAppDelivery(t *testing.T) {
	m := New(Options{}).Manifest
	spec := m.Triggers["changes"]
	if !spec.AppDelivered() || spec.Verify.Header != "intuit-signature" {
		t.Fatalf("spec %+v", spec)
	}
	body := []byte(`[{"specversion":"1.0","id":"e1","type":"qbo.invoice.created.v1","intuitentityid":"130","intuitaccountid":"9341453050298730","time":"2026-10-26T16:01:22Z"},
	  {"specversion":"1.0","id":"e2","type":"qbo.payment.created.v1","intuitentityid":"171","intuitaccountid":"4620816365189098020","time":"2026-10-26T16:01:23Z"}]`)
	mac := hmac.New(sha256.New, []byte("verifier-token-test"))
	mac.Write(body)
	h := http.Header{}
	h.Set("intuit-signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	if err := connector.VerifyWebhook(spec.Verify, "verifier-token-test", h, body); err != nil {
		t.Fatalf("signature: %v", err)
	}
	if err := connector.VerifyWebhook(spec.Verify, "another-token", h, body); err == nil {
		t.Fatal("another app's token verified")
	}
	e := expr.MustNewTriggerEngine("body", "headers", "query", "item")
	var parsed any
	_ = json.Unmarshal(body, &parsed)
	act := map[string]any{"body": parsed, "headers": map[string]any{}, "query": map[string]any{}}
	items, err := e.Eval(spec.Split, act)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items.([]any) {
		a := map[string]any{"body": parsed, "headers": map[string]any{}, "query": map[string]any{}, "item": it}
		ev, err1 := e.Eval(spec.EventType, a)
		acct, err2 := e.Eval(spec.AccountKey, a)
		corr, err3 := e.Eval(spec.Correlation, a)
		if err := errors.Join(err1, err2, err3); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(spec.Events, ev.(string)) {
			t.Errorf("%v is not a declared event", ev)
		}
		got = append(got, ev.(string)+"@"+acct.(string)+"#"+strings.TrimSpace(strings.ReplaceAll(toString(corr), "<nil>", "")))
	}
	if want := []string{"invoice.created@9341453050298730#130", "payment.created@4620816365189098020#"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// The connection check refuses credentials it cannot refresh and store
// (a pasted connection checked before it is saved) in words that say
// what to do.
func TestPastedConnectionsAreRefusedPlainly(t *testing.T) {
	c := New(Options{})
	_, err := c.Actions["get_company"].Execute(context.Background(), connector.Request{Credentials: baseCreds(), HTTP: http.DefaultClient})
	if !errors.Is(err, effects.ErrFatal) || !strings.Contains(err.Error(), "Connect button") {
		t.Fatalf("err %v", err)
	}
}
