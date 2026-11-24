package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLimitersAreBounded(t *testing.T) {
	s := &Server{}
	a := s.limiter("k:a", time.Second, 1)
	if !a.Allow() || a.Allow() || s.limiter("k:a", time.Second, 1) != a {
		t.Fatal("a key does not keep its bucket")
	}
	// A bucket idle long enough to have refilled is dropped.
	s.limiters.m["k:a"].last = time.Now().Add(-2 * time.Minute)
	s.limiters.swept = time.Time{}
	s.limiter("k:b", time.Second, 1)
	if _, ok := s.limiters.m["k:a"]; ok {
		t.Error("an idle bucket was kept")
	}
	// Past the bound, new keys of a kind share one bucket; known keys keep
	// theirs.
	for i := range maxLimiters {
		s.limiter(fmt.Sprint("x:", i), time.Second, 1)
	}
	if n := len(s.limiters.m); n > maxLimiters {
		t.Fatalf("%d buckets", n)
	}
	if s.limiter("y:1", time.Second, 1) != s.limiter("y:2", time.Second, 1) {
		t.Error("overflow keys do not share a bucket")
	}
	if s.limiter("k:b", time.Second, 1) == s.limiter("y:1", time.Second, 1) {
		t.Error("a known key lost its bucket")
	}
}

// Per-address limits see an IPv6 subscriber as one client: its whole /64,
// as the public host's limits do (formguard.ClientKey), so rotating
// addresses within it does not refill the sign-in bucket.
func TestLoginLimitSeesAnIPv6Slash64(t *testing.T) {
	s := &Server{LoginBurst: 2, Logger: slog.New(slog.DiscardHandler)}
	try := func(addr string) int {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader("not json"))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = addr
		w := httptest.NewRecorder()
		s.login(w, r)
		return w.Code
	}
	for i, addr := range []string{"[2001:db8:1:2::1]:4000", "[2001:db8:1:2::2]:4000"} {
		if c := try(addr); c == http.StatusTooManyRequests {
			t.Fatalf("attempt %d limited early", i)
		}
	}
	if c := try("[2001:db8:1:2:ffff::3]:4000"); c != http.StatusTooManyRequests {
		t.Fatalf("a third address in the same /64: %d, want 429", c)
	}
	// Another /64, and IPv4 addresses, have buckets of their own.
	if c := try("[2001:db8:1:3::1]:4000"); c == http.StatusTooManyRequests {
		t.Fatal("another /64 shares the bucket")
	}
	if c := try("198.51.100.7:4000"); c == http.StatusTooManyRequests {
		t.Fatal("an IPv4 address shares the bucket")
	}
	if limitIP(httptest.NewRequest(http.MethodGet, "/", nil)) == "" {
		t.Fatal("no key")
	}
}
