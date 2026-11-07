package publicweb

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/israel-duff/taskiem/engine/formguard"
)

// An IPv6 subscriber holds a whole /64 and can pick any address in it, so
// the page-view limit, the report limit and the reporter's identity see
// the /64, as form submissions do: one person cannot pose as three
// reporters and have a page suspended (F69).
func TestPublicLimitsSeeAnIPv6Slash64(t *testing.T) {
	f := &fakeHosts{sites: map[string]Site{"ada.shops.test": {Tenant: uuid.New(), Kind: "storefront", ID: uuid.New()}}}
	s, h := newServer(f)
	s.FormKey = bytes.Repeat([]byte{7}, 32)
	from := func(addr string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = "ada.shops.test"
		r.RemoteAddr = "[" + addr + "]:4000"
		return r
	}

	// Views: one bucket for the /64.
	s.Views = formguard.NewLimiter(rate.Every(time.Hour), 1, 100, time.Hour)
	for i, addr := range []string{"2001:db8:aa:bb::1", "2001:db8:aa:bb::2"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, from(addr))
		if want := []int{200, 429}[i]; w.Code != want {
			t.Errorf("view from %s: %d, want %d", addr, w.Code, want)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, from("2001:db8:aa:cc::1"))
	if w.Code != 200 {
		t.Errorf("another /64 shares the bucket: %d", w.Code)
	}

	// The reporter's identity.
	a, b, c := s.reportIPHash(from("2001:db8:aa:bb::1")), s.reportIPHash(from("2001:db8:aa:bb::ffff")), s.reportIPHash(from("2001:db8:aa:cc::1"))
	if !bytes.Equal(a, b) {
		t.Error("two addresses in one /64 count as two reporters")
	}
	if bytes.Equal(a, c) {
		t.Error("two /64s count as one reporter")
	}

	// The report limit: one bucket for the /64.
	var took int
	s.Reports = func(_ context.Context, _ Report) (bool, error) { took++; return true, nil }
	for i := range 10 {
		r := httptest.NewRequest(http.MethodPost, "/report", strings.NewReader(url.Values{"reason": {"scam"}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = fmt.Sprintf("[2001:db8:ee:1::%x]:4000", i+1)
		_ = r.ParseForm()
		if _, err := s.takeReport(r, "storefront", uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	if took > 5 {
		t.Errorf("ten addresses in one /64 made %d reports past the limit of 5 a minute", took)
	}
}
