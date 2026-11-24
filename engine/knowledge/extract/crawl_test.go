package extract

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// site serves a small website and records the paths requested.
type site struct {
	mu       sync.Mutex
	requests []string
	agents   map[string]bool
	robots   string
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.URL.RequestURI())
	s.agents[r.UserAgent()] = true
	s.mu.Unlock()
	page := func(title, body string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<html><head><title>%s</title></head><body><main>%s</main></body></html>", title, body)
	}
	long := strings.Repeat("q", 250)
	switch r.URL.Path {
	case "/robots.txt":
		if s.robots == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, s.robots)
	case "/":
		page("Home", `<p>Welcome to the bakery.</p>
<a href="/a">A</a> <a href="/b#x">B</a> <a href="/private/secret">Private</a>
<a href="/menu.pdf">Menu</a> <a href="/data">Data</a> <a href="/broken">Broken</a>
<a href="https://elsewhere.example/">Elsewhere</a> <a href="/login">Log in</a>
<a href="/cart/">Cart</a> <a href="/search?q=`+long+`">Search</a> <a href="/a#again">A again</a>
<a href="/moved">Moved</a>`)
	case "/a":
		page("A", `<p>Page A.</p><a href="/">Home</a> <a href="/b">B</a> <a href="/c">C</a>`)
	case "/b":
		page("B", `<p>Page B.</p><a href="/a">A</a>`)
	case "/c":
		page("C", `<p>Page C.</p>`)
	case "/moved":
		http.Redirect(w, r, "/c", http.StatusMovedPermanently)
	case "/data":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"not":"html"}`)
	case "/broken":
		http.Error(w, "boom", http.StatusInternalServerError)
	default:
		http.NotFound(w, r)
	}
}

func (s *site) requested(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.requests {
		if r == path {
			return true
		}
	}
	return false
}

func newSite(t *testing.T, robots string) (*site, *httptest.Server) {
	s := &site{agents: map[string]bool{}, robots: robots}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func TestCrawl(t *testing.T) {
	s, srv := newSite(t, "User-agent: *\nDisallow: /private\n\nUser-agent: OtherBot\nDisallow: /\n")
	pages, errs := Crawl(context.Background(), srv.Client(), srv.URL, 20)
	var got []string
	for _, p := range pages {
		got = append(got, strings.TrimPrefix(p.URL, srv.URL)+" "+p.Title+": "+p.Text)
	}
	want := []string{
		"/ Home: Welcome to the bakery.\nA B Private Menu Data Broken Elsewhere Log in Cart Search A again Moved",
		"/a A: Page A.\nHome B C",
		"/b B: Page B.\nA",
		"/c C: Page C.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("pages:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "/broken") || !strings.Contains(errs[0].Error(), "500") {
		t.Errorf("errors = %v", errs)
	}
	for _, p := range []string{"/private/secret", "/menu.pdf", "/login", "/cart/"} {
		if s.requested(p) {
			t.Errorf("%s was requested", p)
		}
	}
	if !s.requested("/data") || !s.requested("/moved") {
		t.Errorf("requests = %v", s.requests)
	}
	if len(s.agents) != 1 || !s.agents[UserAgent] {
		t.Errorf("user agents = %v", s.agents)
	}
}

func TestCrawlLimits(t *testing.T) {
	_, srv := newSite(t, "")
	pages, errs := Crawl(context.Background(), srv.Client(), srv.URL+"/", 2)
	if len(pages) != 2 || len(errs) != 0 {
		t.Errorf("maxPages 2: %d pages, errors %v", len(pages), errs)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pages, errs = Crawl(ctx, srv.Client(), srv.URL, 10)
	if len(pages) != 0 || len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
		t.Errorf("cancelled: %d pages, errors %v", len(pages), errs)
	}

	if _, errs := Crawl(context.Background(), srv.Client(), "ftp://example.com/", 10); len(errs) != 1 {
		t.Errorf("bad start: errors %v", errs)
	}

	// A site that keeps every bot out is not read at all.
	s, srv := newSite(t, "User-agent: *\nDisallow: /\n")
	pages, errs = Crawl(context.Background(), srv.Client(), srv.URL, 10)
	if len(pages) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "robots.txt") || s.requested("/") {
		t.Errorf("disallowed: %d pages, errors %v", len(pages), errs)
	}
}

func TestIsTwin(t *testing.T) {
	site := mustURL(t, "http://example.com/")
	for u, want := range map[string]bool{
		"https://www.example.com/":     true,
		"https://example.com/home":     true,
		"http://WWW.Example.com:80/":   true,
		"https://example.com:8443/":    false,
		"https://evil.example.com/":    false,
		"https://example.com.evil.io/": false,
		"ftp://example.com/":           false,
	} {
		if got := isTwin(mustURL(t, u), site); got != want {
			t.Errorf("isTwin(%s) = %v", u, got)
		}
	}
}

func TestCrawlOurRobotsGroup(t *testing.T) {
	// The TaskiemBot group replaces the * group, rather than adding to it.
	s, srv := newSite(t, "User-agent: *\nDisallow: /b\n\nUser-agent: Googlebot\nUser-agent: TaskiemBot\nDisallow: /a\nAllow: /a$\nDisallow: /c\n")
	pages, errs := Crawl(context.Background(), srv.Client(), srv.URL, 20)
	var got []string
	for _, p := range pages {
		got = append(got, p.Title)
	}
	if strings.Join(got, ",") != "Home,A,B" {
		t.Errorf("pages = %v", got)
	}
	// /moved redirects to /c, which robots.txt keeps the bot out of.
	if len(errs) != 3 || !strings.Contains(errs[2].Error(), "not crawled") { // also /private (404) and /broken
		t.Errorf("errors = %v", errs)
	}
	if s.requested("/c") {
		t.Error("/c was requested")
	}
}

func TestRobotsMatch(t *testing.T) {
	for _, c := range []struct {
		pattern, target string
		want            bool
	}{
		{"/private", "/private/x", true},
		{"/private", "/privat", false},
		{"/*.php", "/index.php", true},
		{"/*.php$", "/index.php?x=1", false},
		{"/*.php$", "/index.php", true},
		{"/a$", "/a", true},
		{"/a$", "/ab", false},
		{"/", "/anything", true},
		{"/*/edit", "/posts/1/edit", true},
		{"/*/edit", "/posts/1", false},
	} {
		if got := robotsMatch(c.pattern, c.target); got != c.want {
			t.Errorf("robotsMatch(%q, %q) = %v", c.pattern, c.target, got)
		}
	}
}
