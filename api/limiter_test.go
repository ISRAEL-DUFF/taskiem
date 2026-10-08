package api

import (
	"fmt"
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
