package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/db/dbtest"
	"github.com/israel-duff/taskiem/engine/runtime"
)

// Channel lookups for tenants the URL makes up are paced by the caller's
// address, not by the made-up tenant: a flood from one address is told
// to try again after a burst, and leaves one bucket in the shared
// limiter set rather than one per tenant (F28).
func TestUSSDLookupsPacedPerCaller(t *testing.T) {
	d := dbtest.New(t)
	s := &Server{Store: &runtime.Store{Pool: d.App}, Logger: slog.New(slog.DiscardHandler)}
	h := s.USSDHooks()
	before := len(s.limiters.m)
	codes := map[string]int{}
	start := time.Now()
	for range 40 {
		body := url.Values{"sessionId": {"s1"}, "phoneNumber": {"+2348000000000"}, "serviceCode": {"*384*1#"}, "text": {""}}.Encode()
		r := httptest.NewRequest(http.MethodPost, "/"+uuid.NewString()+"/africastalking", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "203.0.113.9:4000"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		switch {
		case w.Code == http.StatusNotFound:
			codes["unknown"]++
		case w.Code == http.StatusOK && strings.HasPrefix(w.Body.String(), "END"):
			codes["busy"]++
		default:
			t.Fatalf("answer %d %q", w.Code, w.Body.String())
		}
	}
	// The burst, plus the tokens the limiter gave back while the loop ran
	// (one per 100 ms: forty lookups that miss the database take over a
	// second on a loaded runner).
	allowed := 20 + int(time.Since(start)/(100*time.Millisecond)) + 1
	if codes["busy"] == 0 || codes["unknown"] > allowed {
		t.Errorf("40 made-up tenants from one address: %v; want at most the burst (%d) looked up", codes, allowed)
	}
	if n := len(s.limiters.m) - before; n != 1 {
		t.Errorf("one address left %d limiter buckets, want 1", n)
	}
}

// A full channel cache sheds expired and unknown entries before the
// channels real tenants are using (F28).
func TestUSSDChannelCacheKeepsRealChannels(t *testing.T) {
	d := dbtest.New(t)
	s := &Server{Store: &runtime.Store{Pool: d.App}, Logger: slog.New(slog.DiscardHandler)}
	real := uuid.NewString() + "/africastalking"
	s.ussd.channels = map[string]ussdChannelEntry{real: {ch: &ussdChannel{active: true}, at: time.Now()}}
	for len(s.ussd.channels) < ussdCacheMax {
		s.ussd.channels[uuid.NewString()+"/africastalking"] = ussdChannelEntry{at: time.Now()}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/"+uuid.NewString()+"/africastalking", strings.NewReader("sessionId=s1&phoneNumber=%2B2348000000000&serviceCode=*384*1%23&text="))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "198.51.100.7:4000"
	s.USSDHooks().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("lookup answered %d %q", w.Code, w.Body.String())
	}
	if e, ok := s.ussd.channels[real]; !ok || e.ch == nil {
		t.Error("a full cache dropped a real tenant's channel")
	}
	if n := len(s.ussd.channels); n > ussdCacheMax {
		t.Errorf("cache holds %d", n)
	}
}
