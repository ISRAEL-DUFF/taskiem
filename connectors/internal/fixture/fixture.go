// Package fixture replays recorded provider exchanges in tests (spec 6.4:
// connectors ship recorded fixtures; CI replays them against handlers).
package fixture

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/israel-duff/taskiem/engine/conntest"
)

// Exchange is one recorded request and its response. The format is the
// conformance kit's (engine/conntest), which third-party connectors use
// for their own fixtures.
type Exchange = conntest.Exchange

type Request = conntest.Request

type Response = conntest.Response

// Load reads testdata/fixtures/<name>.json.
func Load(t testing.TB, name string) Exchange {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", name+".json")) //nolint:gosec // test fixtures by name
	if err != nil {
		t.Fatal(err)
	}
	var ex Exchange
	if err := json.Unmarshal(raw, &ex); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	ex.Name = name
	return ex
}

// Server serves exchanges in order and fails the test on any mismatch.
type Server struct {
	*httptest.Server
	t     testing.TB
	mu    sync.Mutex
	queue []Exchange
}

// Serve starts a server that expects exactly these exchanges, in order.
func Serve(t testing.TB, exchanges ...Exchange) *Server {
	t.Helper()
	s := &Server{t: t, queue: exchanges}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(func() {
		s.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.queue) > 0 {
			t.Errorf("%d expected request(s) never arrived, next: %s", len(s.queue), s.queue[0].Name)
		}
	})
	return s
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if len(s.queue) == 0 {
		s.mu.Unlock()
		s.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.Error(w, "unexpected", http.StatusTeapot)
		return
	}
	ex := s.queue[0]
	s.queue = s.queue[1:]
	s.mu.Unlock()

	want := ex.Request
	if r.Method != want.Method || r.URL.Path != want.Path {
		s.t.Errorf("%s: got %s %s, want %s %s", ex.Name, r.Method, r.URL.Path, want.Method, want.Path)
	}
	for k, v := range want.Query {
		if got := r.URL.Query().Get(k); got != v {
			s.t.Errorf("%s: query %s = %q, want %q", ex.Name, k, got, v)
		}
	}
	for k, v := range want.Headers {
		if got := r.Header.Get(k); got != v {
			s.t.Errorf("%s: header %s = %q, want %q", ex.Name, k, got, v)
		}
	}
	if want.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		var got any
		if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(norm(got), norm(want.Body)) {
			s.t.Errorf("%s: body %s, want %v", ex.Name, strings.TrimSpace(string(raw)), want.Body)
		}
	}
	for k, v := range ex.Response.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ex.Response.Status)
	_ = json.NewEncoder(w).Encode(ex.Response.Body)
}

func norm(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}
