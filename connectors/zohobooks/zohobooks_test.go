package zohobooks

import (
	"context"
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
)

// Every action passes the conformance kit on fixtures shaped after Zoho's
// published examples (docs/integrations/zoho-books.md).
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
	if len(rep.Uncovered)+len(rep.ReadWrites) > 0 {
		t.Errorf("uncovered %v, reads that write %v", rep.Uncovered, rep.ReadWrites)
	}
}

// The books capabilities and events, through their mappings: the same
// templates as QuickBooks run on Zoho Books.
func TestCapabilities(t *testing.T) {
	fixture.Capabilities(t, func(base string) *connector.Connector { return New(Options{BaseURL: base}) })
}

func TestRegisters(t *testing.T) {
	reg := connector.NewRegistry()
	if err := reg.Register(New(Options{})); err != nil {
		t.Fatal(err)
	}
	c, _ := reg.Get("zoho_books@1")
	m := c.Manifest
	if m.KindOf() != "product" || m.Category != "accounting" || len(m.Capabilities) != 9 || len(m.Events) != 3 || m.ClientName() != "zoho" {
		t.Errorf("kind %s, category %s, %d capabilities, %d events, client %s", m.KindOf(), m.Category, len(m.Capabilities), len(m.Events), m.ClientName())
	}
	// The account's own data centre (rule 19): its servers only.
	creds := map[string]string{"api_domain": "https://www.zohoapis.eu", "accounts_server": "https://accounts.zoho.eu"}
	if h := m.HostsFor(creds); !slices.Equal(h, []string{"www.zohoapis.eu", "accounts.zoho.eu"}) {
		t.Errorf("hosts %v", h)
	}
	creds["api_domain"] = "https://evil.example"
	if h := m.HostsFor(creds); slices.Contains(h, "evil.example") {
		t.Errorf("a host outside Zoho's is reachable: %v", h)
	}
	if got, err := m.OAuthURL(m.Auth.OAuth2.TokenURL, map[string]string{"accounts_server": "https://accounts.zoho.eu"}); err != nil || got != "https://accounts.zoho.eu/oauth/v2/token" {
		t.Errorf("token URL %q %v", got, err)
	}
	if p := m.Triggers["changes"].Poll; p == nil || p.Interval() != 15*time.Minute {
		t.Errorf("poll %+v", p)
	}
}

// fakeZoho is the account's accounts server and Books.
type fakeZoho struct {
	mu       sync.Mutex
	access   string
	issued   atomic.Int32
	orgCalls int
	calls    []string
}

func (f *fakeZoho) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/oauth/v2/token" {
		_ = r.ParseForm()
		if r.PostForm.Get("refresh_token") != "1000.good-refresh" || r.PostForm.Get("client_id") != "1000.TESTCLIENT" || r.PostForm.Get("grant_type") != "refresh_token" {
			_, _ = io.WriteString(w, `{"error":"invalid_code"}`) // as Zoho answers, with 200
			return
		}
		n := f.issued.Add(1)
		f.access = "1000.access-" + string('a'+n)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": f.access, "api_domain": "https://www.zohoapis.com", "token_type": "Bearer", "expires_in": 3600})
		return
	}
	f.calls = append(f.calls, r.URL.Path+"?"+r.URL.RawQuery)
	if r.Header.Get("Authorization") != "Zoho-oauthtoken "+f.access {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code":57,"message":"You are not authorized to perform this operation"}`)
		return
	}
	switch r.URL.Path {
	case "/organizations":
		f.orgCalls++
		_, _ = io.WriteString(w, `{"code":0,"message":"success","organizations":[{"organization_id":"10234700","name":"Ada Catering","is_default_org":false},{"organization_id":"10234695","name":"Ada Foods Ltd","is_default_org":true}]}`)
	case "/contacts":
		_, _ = io.WriteString(w, `{"code":0,"message":"success","contacts":[]}`)
	default:
		http.NotFound(w, r)
	}
}

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

func (s *store) copy() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.creds {
		out[k] = v
	}
	return out
}

func baseCreds() map[string]string {
	return map[string]string{"refresh_token": "1000.good-refresh", "client_id": "1000.TESTCLIENT", "client_secret": "secret"}
}

// Concurrent workers ask Zoho for one access token between them (Zoho
// gives ten every ten minutes), and the default organisation is chosen
// once and kept.
func TestTokensAndTheOrganisationAreKept(t *testing.T) {
	f := &fakeZoho{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL, AccountsServer: srv.URL})
	st := &store{creds: baseCreds()}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Actions["find_contact"].Execute(context.Background(), connector.Request{Input: map[string]any{"email": "ada@example.com"}, Credentials: st.copy(), HTTP: srv.Client(), Rotate: st.rotate})
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
	if n := f.issued.Load(); n != 1 {
		t.Errorf("%d access tokens issued, want 1", n)
	}
	got := st.copy()
	if got["access_token"] != f.access || got["access_expires_at"] == "" || got["organization_id"] != "10234695" || got["organization_name"] != "Ada Foods Ltd" {
		t.Errorf("stored %v", got)
	}
	// Every later call names it without asking again.
	before := f.orgCalls
	if _, err := c.Actions["find_contact"].Execute(context.Background(), connector.Request{Input: map[string]any{"email": "ada@example.com"}, Credentials: st.copy(), HTTP: srv.Client(), Rotate: st.rotate}); err != nil {
		t.Fatal(err)
	}
	if f.orgCalls != before || !strings.Contains(f.calls[len(f.calls)-1], "organization_id=10234695") {
		t.Errorf("organisations asked %d more times; last call %s", f.orgCalls-before, f.calls[len(f.calls)-1])
	}
}

// An access token Zoho refuses is refreshed once; a refresh token it
// refuses (200 and invalid_code) expires the connection, storing nothing.
func TestRefusedTokens(t *testing.T) {
	f := &fakeZoho{access: "1000.current"}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := New(Options{BaseURL: srv.URL, AccountsServer: srv.URL})
	creds := baseCreds()
	creds["organization_id"] = "10234695"
	creds["access_token"], creds["access_expires_at"] = "1000.revoked", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	st := &store{creds: creds}
	if _, err := c.Actions["find_contact"].Execute(context.Background(), connector.Request{Input: map[string]any{"email": "ada@example.com"}, Credentials: st.copy(), HTTP: srv.Client(), Rotate: st.rotate}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 || f.issued.Load() != 1 {
		t.Errorf("calls %v, tokens %d", f.calls, f.issued.Load())
	}

	spent := &store{creds: baseCreds()}
	spent.creds["refresh_token"] = "1000.revoked-refresh"
	_, err := c.Actions["list_organizations"].Execute(context.Background(), connector.Request{Credentials: spent.copy(), HTTP: srv.Client(), Rotate: spent.rotate})
	if !errors.Is(err, connector.ErrCredentialsRejected) || !errors.Is(err, effects.ErrFatal) || !strings.Contains(err.Error(), "connect Zoho Books again") || spent.saves != 0 {
		t.Fatalf("err %v, saves %d", err, spent.saves)
	}
}

// A connection whose servers are not Zoho's sends nothing there.
func TestServersOutsideZohoAreRefused(t *testing.T) {
	c := New(Options{})
	creds := baseCreds()
	creds["api_domain"], creds["accounts_server"] = "https://www.zohoapis.evil.example", "https://accounts.zoho.com"
	creds["access_token"], creds["access_expires_at"] = "1000.x", "2099-01-01T00:00:00Z"
	_, err := c.Actions["list_organizations"].Execute(context.Background(), connector.Request{Credentials: creds, HTTP: http.DefaultClient})
	if !errors.Is(err, effects.ErrFatal) || !strings.Contains(err.Error(), "not Zoho's") {
		t.Fatalf("err %v", err)
	}
}

func TestTimes(t *testing.T) {
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	if zohoTime(at) != "2026-10-26T09:00:00+0000" {
		t.Errorf("zohoTime %s", zohoTime(at))
	}
	if !after("2026-10-26T10:00:01+0100", at) || after("2026-10-26T09:59:59+0100", at) || after("", at) {
		t.Error("after")
	}
}
