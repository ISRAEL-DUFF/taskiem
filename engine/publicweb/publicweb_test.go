package publicweb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/formguard"
	"github.com/israel-duff/taskiem/engine/httpsec"
)

type fakeHosts struct {
	sites map[string]Site
	calls atomic.Int64
	err   error
}

func (f *fakeHosts) lookup(_ context.Context, host string) (Site, bool, error) {
	f.calls.Add(1)
	if f.err != nil {
		return Site{}, false, f.err
	}
	s, ok := f.sites[host]
	return s, ok, nil
}

func newServer(f *fakeHosts) (*Server, http.Handler) {
	// No view limit to speak of: TestViewLimit tests it.
	s := &Server{Lookup: f.lookup, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Inf, 1, 100, time.Hour)}
	return s, s.Handler()
}

func get(h http.Handler, method, host, path string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Host = host
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRouting(t *testing.T) {
	f := &fakeHosts{sites: map[string]Site{
		"ada.shops.test":    {Tenant: uuid.New(), Kind: "storefront", ID: uuid.New()},
		"closed.shops.test": {Tenant: uuid.New(), Kind: "storefront", ID: uuid.New(), Suspended: true},
	}}
	_, h := newServer(f)
	for _, c := range []struct {
		method, host, path string
		status             int
		body               string
	}{
		{"GET", "ada.shops.test", "/", 200, "Coming soon"},
		{"GET", "ADA.shops.test:443", "/", 200, "Coming soon"},
		{"GET", "ada.shops.test.", "/", 200, "Coming soon"},
		{"HEAD", "ada.shops.test", "/", 200, ""},
		{"GET", "ada.shops.test", "/admin", 404, "Page not found"},
		{"POST", "ada.shops.test", "/", 405, "Page not found"},
		{"GET", "closed.shops.test", "/", 410, "unavailable"},
		{"GET", "nobody.shops.test", "/", 404, "Page not found"},
		{"GET", "app.taskiem.test", "/v1/runs", 404, "Page not found"},
	} {
		w := get(h, c.method, c.host, c.path)
		if w.Code != c.status || !strings.Contains(w.Body.String(), c.body) {
			t.Errorf("%s %s%s: %d %q", c.method, c.host, c.path, w.Code, w.Body.String())
		}
	}
}

func TestHeadersAndNoCookies(t *testing.T) {
	f := &fakeHosts{sites: map[string]Site{"ada.shops.test": {Kind: "storefront"}}}
	_, h := newServer(f)
	for _, host := range []string{"ada.shops.test", "nobody.shops.test"} {
		w := get(h, "GET", host, "/")
		hd := w.Header()
		if hd.Get("Content-Security-Policy") != httpsec.PublicCSP || hd.Get("X-Frame-Options") != "DENY" ||
			hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: headers %v", host, hd)
		}
		if hd.Get("Set-Cookie") != "" {
			t.Errorf("%s: set a cookie", host)
		}
	}

	// A handler that tries to set a cookie has it dropped.
	s := &Server{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	guarded := s.noCookies(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "track", Value: "1"})
		_, _ = w.Write([]byte("hi"))
	}))
	w := httptest.NewRecorder()
	guarded.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Header().Get("Set-Cookie") != "" || w.Body.String() != "hi" {
		t.Fatalf("cookie not dropped: %v %q", w.Header(), w.Body.String())
	}
}

func TestHostCache(t *testing.T) {
	f := &fakeHosts{sites: map[string]Site{"ada.shops.test": {Kind: "storefront"}}}
	s, h := newServer(f)
	now := time.Now()
	s.now = func() time.Time { return now }
	for range 50 {
		get(h, "GET", "ada.shops.test", "/")
		get(h, "GET", "nobody.shops.test", "/")
	}
	if n := f.calls.Load(); n != 2 {
		t.Fatalf("lookups = %d, want one per host while cached", n)
	}
	now = now.Add(MissTTL + time.Second)
	get(h, "GET", "ada.shops.test", "/")
	get(h, "GET", "nobody.shops.test", "/")
	if n := f.calls.Load(); n != 3 {
		t.Fatalf("lookups = %d: a miss is looked up again after MissTTL, a hit is not", n)
	}

	// A flood of made-up hosts cannot grow the cache past its bound.
	for i := range HostCacheSize * 2 {
		if _, _, err := s.site(context.Background(), fmt.Sprintf("x%d.shops.test", i)); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.hosts.Len(); n > HostCacheSize {
		t.Fatalf("cache holds %d hosts, bound %d", n, HostCacheSize)
	}
}

func TestLookupFailure(t *testing.T) {
	f := &fakeHosts{err: errors.New("database down")}
	_, h := newServer(f)
	if w := get(h, "GET", "ada.shops.test", "/"); w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("lookup failure: %d %v", w.Code, w.Header())
	}
	if w := get(h, "GET", "ada.shops.test", "/"); w.Code != 503 {
		t.Fatal("a failed lookup must not be cached")
	}
	if f.calls.Load() != 2 {
		t.Fatalf("calls = %d", f.calls.Load())
	}
}

func TestETagAndStatic(t *testing.T) {
	f := &fakeHosts{sites: map[string]Site{"ada.shops.test": {Kind: "storefront"}}}
	_, h := newServer(f)
	w := get(h, "GET", "ada.shops.test", "/")
	etag := w.Header().Get("ETag")
	if etag == "" || !strings.Contains(w.Header().Get("Cache-Control"), "s-maxage") {
		t.Fatalf("headers %v", w.Header())
	}
	if w := get(h, "GET", "ada.shops.test", "/", "If-None-Match", etag); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("revalidation: %d", w.Code)
	}

	css := regexp.MustCompile(`href="(/_static/site\.[0-9a-f]{10}\.css)"`).FindStringSubmatch(w.Body.String())
	if css == nil {
		t.Fatalf("no hashed stylesheet link in %q", w.Body.String())
	}
	sw := get(h, "GET", "any.host.test", css[1])
	if sw.Code != 200 || !strings.HasPrefix(sw.Header().Get("Content-Type"), "text/css") ||
		!strings.Contains(sw.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("static: %d %v", sw.Code, sw.Header())
	}
	if w := get(h, "GET", "any.host.test", "/_static/site.css"); w.Code != 404 {
		t.Fatal("unhashed static names must not be served")
	}
	if w := get(h, "GET", "any.host.test", "/_static/../publicweb.go"); w.Code != 404 {
		t.Fatal("static path escaped")
	}
}

func TestTLSAsk(t *testing.T) {
	f := &fakeHosts{sites: map[string]Site{"ada.shops.test": {Kind: "storefront"}, "closed.shops.test": {Suspended: true}}}
	s, _ := newServer(f)
	for _, c := range []struct {
		q      string
		status int
	}{{"domain=ada.shops.test", 200}, {"domain=ADA.shops.test.", 200}, {"domain=closed.shops.test", 200}, {"domain=evil.test", 404}, {"", 400}} {
		w := httptest.NewRecorder()
		s.TLSAsk(w, httptest.NewRequest("GET", "/tls/ask?"+c.q, nil))
		if w.Code != c.status {
			t.Errorf("%q: %d", c.q, w.Code)
		}
	}
}

func TestViewLimit(t *testing.T) {
	f := &fakeHosts{sites: map[string]Site{"ada.shops.test": {Kind: "storefront"}}}
	s := &Server{Lookup: f.lookup, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Views: formguard.NewLimiter(rate.Every(time.Hour), 3, 100, time.Hour)}
	h := s.Handler()
	codes := map[int]int{}
	for range 5 {
		codes[get(h, "GET", "ada.shops.test", "/").Code]++
	}
	if codes[200] != 3 || codes[429] != 2 {
		t.Fatalf("codes %v", codes)
	}
	// Static files are not counted.
	w := get(h, "GET", "ada.shops.test", "/")
	css := regexp.MustCompile(`/_static/site\.[0-9a-f]{10}\.css`)
	if w.Code != 429 || css.FindString(w.Body.String()) == "" {
		t.Fatalf("limited page: %d", w.Code)
	}
	if sw := get(h, "GET", "ada.shops.test", css.FindString(w.Body.String())); sw.Code != 200 {
		t.Fatalf("static under a limit: %d", sw.Code)
	}
	// Another address has its own bucket.
	r := httptest.NewRequest("GET", "/", nil)
	r.Host, r.RemoteAddr = "ada.shops.test", "102.89.3.4:5000"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("another address: %d", w.Code)
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:4431"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 102.89.1.2")
	if ip := (&Server{}).ClientIP(r); ip != "10.0.0.5" {
		t.Fatalf("untrusted proxy: %s", ip)
	}
	if ip := (&Server{TrustProxy: true}).ClientIP(r); ip != "102.89.1.2" {
		t.Fatalf("trusted proxy: %s", ip)
	}
}

func TestEmbedFraming(t *testing.T) {
	withSites, noSites, broken := uuid.New(), uuid.New(), uuid.New()
	f := &fakeHosts{sites: map[string]Site{
		"ada.shops.test":  {Tenant: withSites, Kind: "storefront"},
		"bola.shops.test": {Tenant: noSites, Kind: "storefront"},
		"odd.shops.test":  {Tenant: broken, Kind: "storefront"},
	}}
	s, _ := newServer(f)
	var calls atomic.Int64
	s.Origins = func(_ context.Context, tenant uuid.UUID) ([]string, error) {
		calls.Add(1)
		switch tenant {
		case withSites:
			return []string{"https://adakitchen.ng", "https://www.adakitchen.ng"}, nil
		case broken:
			return nil, errors.New("database down")
		}
		return nil, nil
	}
	h := s.Handler()
	w := get(h, "GET", "ada.shops.test", "/embed/catalogue")
	csp := w.Header().Get("Content-Security-Policy")
	if w.Code != 200 || !strings.Contains(csp, "frame-ancestors https://adakitchen.ng https://www.adakitchen.ng") || w.Header().Get("X-Frame-Options") != "" {
		t.Fatalf("verified sites: %d %q %q", w.Code, csp, w.Header().Get("X-Frame-Options"))
	}
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("the rest of the policy must stay: %q", csp)
	}
	for _, host := range []string{"bola.shops.test", "odd.shops.test"} {
		w := get(h, "GET", host, "/embed/catalogue")
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || w.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s must not be frameable: %v", host, w.Header())
		}
	}
	// Other pages are never frameable, even for a tenant with sites.
	if w := get(h, "GET", "ada.shops.test", "/"); !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		w.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("the storefront page: %v", w.Header())
	}
	// Cached: a second embed view does not look the sites up again.
	n := calls.Load()
	get(h, "GET", "ada.shops.test", "/embed/order")
	if calls.Load() != n {
		t.Fatal("origins looked up again within the cache's life")
	}
}
